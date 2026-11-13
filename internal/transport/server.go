package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LankiYang/golden-hour/internal/game"
	"github.com/pion/webrtc/v4"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	webtransport "github.com/quic-go/webtransport-go"
)

type Config struct {
	Addr, HTTPAddr, PublicURL, Root, CertHash string
	Dev                                       bool
	TLS                                       *tls.Config
	Origins                                   []string
	RTCAddr, RTCPublicIP                      string
	ICEServers                                []webrtc.ICEServer
}
type Server struct {
	Hub       *game.Hub
	config    Config
	WT        *webtransport.Server
	HTTP, TCP *http.Server
	sessions  atomic.Int32
	rtc       *rtcServer
}

func New(hub *game.Hub, c Config) *Server {
	s := &Server{Hub: hub, config: c}
	s.rtc = newRTCServer(s)
	mux := http.NewServeMux()
	h3 := &http3.Server{Addr: c.Addr, TLSConfig: http3.ConfigureTLSConfig(c.TLS), Handler: mux, QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true, MaxIncomingStreams: 4, MaxIncomingUniStreams: 4, MaxIdleTimeout: 25 * time.Second, KeepAlivePeriod: 5 * time.Second, InitialStreamReceiveWindow: 64 << 10, MaxStreamReceiveWindow: 128 << 10, MaxConnectionReceiveWindow: 512 << 10}}
	webtransport.ConfigureHTTP3Server(h3)
	// QUIC stream limits do not grant WebTransport session credit. Safari waits
	// for explicit initial stream/data SETTINGS before opening the room stream.
	s.WT = &webtransport.Server{H3: h3, CheckOrigin: s.originOK, Config: &webtransport.Config{
		MaxIncomingStreams: 4, MaxIncomingUniStreams: 4, MaxIncomingData: 256 << 10,
	}}
	mux.HandleFunc("/connect", s.connect)
	mux.HandleFunc("/api/rtc/offer", s.rtc.offer)
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		s.json(w, game.Event{"url": strings.TrimRight(c.PublicURL, "/") + "/connect", "certificateHash": c.CertHash, "development": c.Dev, "protocol": 1, "tickRate": game.TickRate,
			"rtc": game.Event{"url": "/api/rtc/offer", "iceServers": c.ICEServers}})
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		info := hub.Info()
		info["transports"] = []string{"webtransport", "webrtc"}
		s.json(w, info)
	})
	assets, err := loadStatic(c.Root)
	if err != nil {
		panic(fmt.Errorf("load game assets: %w", err))
	}
	mux.Handle("/", assets)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
	h3.Handler = handler
	s.TCP = &http.Server{Addr: c.Addr, Handler: handler, TLSConfig: c.TLS, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	if c.HTTPAddr != "" {
		s.HTTP = &http.Server{Addr: c.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	}
	return s
}
func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (s *Server) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, allowed := range s.config.Origins {
		if origin == allowed {
			return true
		}
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host == r.Host
}
func (s *Server) Run(ctx context.Context) error {
	if err := s.rtc.start(); err != nil {
		return err
	}
	errCh := make(chan error, 3)
	go func() { errCh <- s.WT.ListenAndServe() }()
	go func() { errCh <- s.TCP.ListenAndServeTLS("", "") }()
	if s.HTTP != nil {
		go func() { errCh <- s.HTTP.ListenAndServe() }()
	}
	ticker := time.NewTicker(time.Second / game.TickRate)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			s.Hub.Tick(now)
		case <-ctx.Done():
			s.Close()
			return nil
		case err := <-errCh:
			s.Close()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
}
func (s *Server) Close() {
	s.rtc.close()
	s.WT.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.TCP.Shutdown(ctx)
	if s.HTTP != nil {
		s.HTTP.Shutdown(ctx)
	}
}

type queuedEvent struct {
	value game.Event
	done  chan error
}
type peer struct {
	sess      *webtransport.Session
	stream    *webtransport.Stream
	events    chan queuedEvent
	snapshots chan []byte
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
}

func (p *peer) Send(e game.Event) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.events <- queuedEvent{value: e}:
		return true
	default:
		p.Close("接收速度过慢")
		return false
	}
}
func (p *peer) Snapshot(b []byte) {
	select {
	case p.snapshots <- b:
	default:
		select {
		case <-p.snapshots:
		default:
		}
		select {
		case p.snapshots <- b:
		default:
		}
	}
}
func (p *peer) Close(message string) {
	p.once.Do(func() { p.cancel(); go p.sess.CloseWithError(0, message) })
}
func (p *peer) writeEvents() {
	encoder := json.NewEncoder(p.stream)
	for {
		select {
		case <-p.ctx.Done():
			return
		case e := <-p.events:
			p.stream.SetWriteDeadline(time.Now().Add(3 * time.Second))
			err := encoder.Encode(e.value)
			if e.done != nil {
				e.done <- err
			}
			if err != nil {
				p.Close("可靠流已关闭")
				return
			}
		}
	}
}
func (p *peer) writeSnapshots() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case b := <-p.snapshots:
			if err := p.sess.SendDatagram(b); err != nil {
				if p.ctx.Err() != nil {
					return
				}
			}
		}
	}
}

type rateLimit struct {
	tokens      float64
	last        time.Time
	rate, burst float64
}

func limiter(rate, burst float64) *rateLimit {
	return &rateLimit{tokens: burst, last: time.Now(), rate: rate, burst: burst}
}

func decodeInput(data []byte) (game.Input, bool) {
	if len(data) != 25 || binary.LittleEndian.Uint16(data) != 0x4748 || data[2] != 1 || data[3] != 1 {
		return game.Input{}, false
	}
	input := game.Input{
		Type:    "input",
		Seq:     binary.LittleEndian.Uint32(data[4:]),
		Forward: float64(math.Float32frombits(binary.LittleEndian.Uint32(data[8:]))),
		Right:   float64(math.Float32frombits(binary.LittleEndian.Uint32(data[12:]))),
		Yaw:     float64(math.Float32frombits(binary.LittleEndian.Uint32(data[16:]))),
		Pitch:   float64(math.Float32frombits(binary.LittleEndian.Uint32(data[20:]))),
		Buttons: data[24],
	}
	for _, value := range [...]float64{input.Forward, input.Right, input.Yaw, input.Pitch} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return game.Input{}, false
		}
	}
	return input, true
}
func (l *rateLimit) allow() bool {
	now := time.Now()
	l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if s.sessions.Add(1) > 256 {
		s.sessions.Add(-1)
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	defer s.sessions.Add(-1)
	sess, err := s.WT.Upgrade(w, r)
	if err != nil {
		log.Printf("WebTransport upgrade: %v", err)
		return
	}
	defer sess.CloseWithError(0, "")
	ctx, cancel := context.WithTimeout(sess.Context(), 10*time.Second)
	stream, err := sess.AcceptStream(ctx)
	cancel()
	if err != nil {
		return
	}
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4096), 16<<10)
	if !scanner.Scan() {
		return
	}
	var hello game.Hello
	if len(scanner.Bytes()) > 2048 || json.Unmarshal(scanner.Bytes(), &hello) != nil || hello.Type != "hello" {
		return
	}
	ctx, cancel = context.WithCancel(sess.Context())
	defer cancel()
	p := &peer{sess: sess, stream: stream, events: make(chan queuedEvent, 128), snapshots: make(chan []byte, 1), ctx: ctx, cancel: cancel}
	binding, err := s.Hub.Join(hello, p)
	if err != nil {
		stream.SetWriteDeadline(time.Now().Add(time.Second))
		json.NewEncoder(stream).Encode(game.Event{"type": "error", "message": err.Error()})
		stream.Close()
		select {
		case <-sess.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
		return
	}
	defer s.Hub.Disconnect(binding)
	defer p.Close("")
	log.Printf("player connected: %s", binding)
	stream.SetReadDeadline(time.Time{})
	go p.writeEvents()
	go p.writeSnapshots()
	go func() {
		rate := limiter(120, 180)
		for {
			data, err := sess.ReceiveDatagram(ctx)
			if err != nil {
				return
			}
			if len(data) > 1200 || !rate.allow() {
				continue
			}
			in, ok := decodeInput(data)
			if !ok {
				ok = json.Unmarshal(data, &in) == nil
			}
			if ok {
				s.Hub.ApplyInput(binding, in)
			}
		}
	}()
	rate := limiter(30, 60)
	for scanner.Scan() {
		if !rate.allow() {
			p.Close("指令发送过快")
			return
		}
		var c game.Command
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			p.Send(game.Event{"type": "error", "message": "指令格式错误"})
			continue
		}
		if c.Type == "leave" {
			s.Hub.Leave(binding)
			written := make(chan error, 1)
			select {
			case p.events <- queuedEvent{value: game.Event{"type": "left"}, done: written}:
			case <-ctx.Done():
				return
			}
			select {
			case <-written:
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			return
		}
		s.Hub.Handle(binding, c)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("player %s: %v", binding, err)
	}
}
func (s *Server) String() string {
	return fmt.Sprintf("%s (UDP HTTP/3 + TCP HTTPS)", s.config.PublicURL)
}
