package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LankiYang/golden-hour/internal/game"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// ICE and DTLS terminate at the authoritative server, never another player.
type rtcServer struct {
	server *Server
	api    *webrtc.API
	mux    *ice.UDPMuxDefault
	mu     sync.Mutex
	closed bool
	peers  map[*rtcPeer]struct{}
}

func newRTCServer(server *Server) *rtcServer {
	return &rtcServer{server: server, peers: make(map[*rtcPeer]struct{})}
}
func (s *rtcServer) start() error {
	addr := s.server.config.RTCAddr
	if addr == "" {
		addr = ":0"
	}
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen WebRTC: %w", err)
	}
	s.mux = ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: conn})
	settings := webrtc.SettingEngine{}
	settings.SetICEUDPMux(s.mux)
	settings.SetIncludeLoopbackCandidate(true)
	settings.SetSCTPMaxMessageSize(16 << 10)
	settings.SetSCTPMaxReceiveBufferSize(256 << 10)
	if ip := s.server.config.RTCPublicIP; ip != "" {
		settings.SetNAT1To1IPs([]string{ip}, webrtc.ICECandidateTypeHost)
	}
	s.api = webrtc.NewAPI(webrtc.WithSettingEngine(settings))
	log.Printf("WebRTC DataChannel fallback: UDP %s", conn.LocalAddr())
	return nil
}
func (s *rtcServer) close() {
	s.mu.Lock()
	s.closed = true
	peers := make([]*rtcPeer, 0, len(s.peers))
	for p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		p.Close("")
	}
	if s.mux != nil {
		s.mux.Close()
	}
}

func (s *rtcServer) offer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.server.originOK(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil || offer.Type != webrtc.SDPTypeOffer || len(offer.SDP) > 60<<10 {
		http.Error(w, "invalid offer", http.StatusBadRequest)
		return
	}
	if s.server.sessions.Add(1) > 256 {
		s.server.sessions.Add(-1)
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{ICEServers: s.server.config.ICEServers})
	if err != nil {
		s.server.sessions.Add(-1)
		http.Error(w, "WebRTC unavailable", 503)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &rtcPeer{pc: pc, owner: s, ctx: ctx, cancel: cancel, events: make(chan queuedEvent, 128), snapshots: make(chan []byte, 1), messages: make(chan rtcMessage, 128), ready: make(chan struct{}), opened: make(map[string]bool)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		pc.Close()
		s.server.sessions.Add(-1)
		http.Error(w, "server stopping", 503)
		return
	}
	s.peers[p] = struct{}{}
	s.mu.Unlock()
	go p.run()
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			p.Close("")
		}
		if state == webrtc.PeerConnectionStateDisconnected {
			time.AfterFunc(3*time.Second, func() {
				if pc.ConnectionState() == webrtc.PeerConnectionStateDisconnected {
					p.Close("")
				}
			})
		}
	})
	pc.OnDataChannel(p.channel)
	ok := false
	defer func() {
		if !ok {
			p.Close("")
		}
	}()
	if err := pc.SetRemoteDescription(offer); err != nil {
		http.Error(w, "invalid SDP", 400)
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "cannot create answer", 400)
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, "cannot set answer", 400)
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-gathered:
	case <-r.Context().Done():
		return
	case <-ctx.Done():
		http.Error(w, "connection closed", 503)
		return
	case <-timer.C:
		http.Error(w, "ICE gathering timeout", 504)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pc.LocalDescription()); err != nil {
		return
	}
	ok = true
}

type rtcMessage struct {
	data  []byte
	state bool
}
type rtcPeer struct {
	pc              *webrtc.PeerConnection
	owner           *rtcServer
	ctx             context.Context
	cancel          context.CancelFunc
	once            sync.Once
	mu              sync.Mutex
	reliable, state *webrtc.DataChannel
	opened          map[string]bool
	ready           chan struct{}
	events          chan queuedEvent
	snapshots       chan []byte
	messages        chan rtcMessage
}

func (p *rtcPeer) Close(_ string) { p.once.Do(func() { p.cancel(); go p.pc.Close() }) }
func (p *rtcPeer) channel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	valid := dc.Protocol() == "gh-pvp-v1" && dc.MaxPacketLifeTime() == nil
	switch dc.Label() {
	case "reliable":
		valid = valid && p.reliable == nil && dc.Ordered() && dc.MaxRetransmits() == nil
		if valid {
			p.reliable = dc
		}
	case "state":
		valid = valid && p.state == nil && !dc.Ordered() && dc.MaxRetransmits() != nil && *dc.MaxRetransmits() == 0
		if valid {
			p.state = dc
		}
	default:
		valid = false
	}
	p.mu.Unlock()
	if !valid {
		p.Close("invalid data channel")
		return
	}
	dc.OnClose(func() { p.Close("") })
	dc.OnError(func(error) { p.Close("") })
	dc.OnOpen(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.opened[dc.Label()] = true
		if len(p.opened) == 2 {
			close(p.ready)
		}
	})
	dc.OnMessage(func(message webrtc.DataChannelMessage) {
		state := dc.Label() == "state"
		limit := 16 << 10
		if state {
			limit = 1200
		}
		if len(message.Data) > limit || message.IsString == state {
			p.Close("invalid message")
			return
		}
		select {
		case p.messages <- rtcMessage{data: message.Data, state: state}:
		case <-p.ctx.Done():
		default:
			p.Close("input queue full")
		}
	})
}
func (p *rtcPeer) Send(event game.Event) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.events <- queuedEvent{value: event}:
		return true
	default:
		p.Close("slow client")
		return false
	}
}
func (p *rtcPeer) Snapshot(data []byte) {
	select {
	case p.snapshots <- data:
	default:
		select {
		case <-p.snapshots:
		default:
		}
		select {
		case p.snapshots <- data:
		default:
		}
	}
}
func (p *rtcPeer) write() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case event := <-p.events:
			data, err := json.Marshal(event.value)
			if err == nil && p.reliable.BufferedAmount() > 64<<10 {
				err = fmt.Errorf("slow reliable channel")
			}
			if err == nil {
				err = p.reliable.SendText(string(data) + "\n")
			}
			if event.done != nil {
				event.done <- err
			}
			if err != nil {
				p.Close("")
				return
			}
		case data := <-p.snapshots:
			// Drop stale snapshots before SCTP queues grow; newest state wins.
			if p.state.BufferedAmount() <= 8<<10 {
				if err := p.state.Send(data); err != nil {
					p.Close("")
					return
				}
			}
		}
	}
}
func (p *rtcPeer) run() {
	defer func() {
		p.Close("")
		p.owner.mu.Lock()
		delete(p.owner.peers, p)
		p.owner.mu.Unlock()
		p.owner.server.sessions.Add(-1)
	}()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-p.ready:
	case <-p.ctx.Done():
		return
	case <-timer.C:
		return
	}
	go p.write()
	var first rtcMessage
	select {
	case first = <-p.messages:
	case <-p.ctx.Done():
		return
	case <-timer.C:
		return
	}
	var hello game.Hello
	if first.state || len(first.data) > 2048 || json.Unmarshal(first.data, &hello) != nil || hello.Type != "hello" {
		return
	}
	hub := p.owner.server.Hub
	binding, err := hub.Join(hello, p)
	if err != nil {
		p.Send(game.Event{"type": "error", "message": err.Error()})
		select {
		case <-p.ctx.Done():
		case <-time.After(150 * time.Millisecond):
		}
		return
	}
	defer hub.Disconnect(binding)
	log.Printf("WebRTC player connected: %s", binding)
	commands, inputs := limiter(30, 60), limiter(120, 180)
	for {
		select {
		case <-p.ctx.Done():
			return
		case message := <-p.messages:
			if message.state {
				if !inputs.allow() {
					continue
				}
				if input, ok := decodeInput(message.data); ok {
					hub.ApplyInput(binding, input)
				}
				continue
			}
			if !commands.allow() {
				return
			}
			var command game.Command
			if json.Unmarshal(message.data, &command) != nil {
				p.Send(game.Event{"type": "error", "message": "指令格式错误"})
				continue
			}
			if command.Type == "leave" {
				hub.Leave(binding)
				written := make(chan error, 1)
				select {
				case p.events <- queuedEvent{value: game.Event{"type": "left"}, done: written}:
				case <-p.ctx.Done():
					return
				}
				select {
				case <-written:
				case <-p.ctx.Done():
					return
				case <-time.After(time.Second):
					return
				}
				select {
				case <-p.ctx.Done():
				case <-time.After(time.Second):
				}
				return
			}
			hub.Handle(binding, command)
		}
	}
}
