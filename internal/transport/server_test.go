package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/LankiYang/golden-hour/internal/certutil"
	"github.com/LankiYang/golden-hour/internal/game"
	webtransport "github.com/quic-go/webtransport-go"
)

type client struct {
	transport *webtransport.Transport
	sess      *webtransport.Session
	stream    *webtransport.Stream
	scan      *bufio.Scanner
}

func (c *client) send(t *testing.T, v any) {
	t.Helper()
	if err := json.NewEncoder(c.stream).Encode(v); err != nil {
		t.Fatal(err)
	}
}
func (c *client) event(t *testing.T, kind string) map[string]any {
	t.Helper()
	c.stream.SetReadDeadline(time.Now().Add(4 * time.Second))
	for c.scan.Scan() {
		var e map[string]any
		if err := json.Unmarshal(c.scan.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if e["type"] == kind {
			return e
		}
	}
	t.Fatalf("waiting for %s: %v", kind, c.scan.Err())
	return nil
}
func (c *client) close() { c.sess.CloseWithError(0, ""); c.transport.Close() }

func TestRealWebTransportTwoClients(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	origin := "https://" + addr
	cert, hash, _, err := certutil.Load("", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	w := &game.World{Colliders: []game.Collider{{Min: [3]float64{-100, -1, -100}, Max: [3]float64{100, 0, 100}}}}
	w.Build()
	hub := game.NewHub(w)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	s := New(hub, Config{Addr: addr, PublicURL: origin, Root: root, CertHash: hash, Dev: true, TLS: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}, Origins: []string{origin}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	}()
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		resp, err := httpClient.Get(origin + "/api/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("HTTPS not listening")
	}
	for _, path := range []string{"/.git/config", "/.local/dev-key.pem", "/cmd/server/main.go"} {
		resp, err := httpClient.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("private path %s exposed: %d", path, resp.StatusCode)
		}
	}
	dial := func(hello game.Hello) *client {
		t.Helper()
		tr := &webtransport.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_, sess, err := tr.Dial(ctx, origin+"/connect", http.Header{"Origin": []string{origin}})
		if err != nil {
			t.Fatal("HTTP/3 WebTransport dial", err)
		}
		stream, err := sess.OpenStreamSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		c := &client{tr, sess, stream, bufio.NewScanner(stream)}
		c.send(t, hello)
		return c
	}
	a := dial(game.Hello{Type: "hello", Create: true, Name: "Alice"})
	defer a.close()
	welcome := a.event(t, "welcome")
	code := welcome["room"].(string)
	id := uint32(welcome["id"].(float64))
	resume := welcome["resume"].(string)
	b := dial(game.Hello{Type: "hello", Room: code, Name: "Bob"})
	defer b.close()
	wb := b.event(t, "welcome")
	bid := uint32(wb["id"].(float64))
	a.send(t, game.Command{Type: "ready", Ready: true})
	b.send(t, game.Command{Type: "ready", Ready: true})
	// Wait until both ready commands have been applied, independent of goroutine ordering.
	for deadline := time.Now().Add(time.Second); ; {
		hub.Lock()
		r := hub.Rooms[code]
		all := r.Players[id].Ready && r.Players[bid].Ready
		hub.Unlock()
		if all {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ready messages not applied")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.send(t, game.Command{Type: "start"})
	a.event(t, "round-start")
	b.event(t, "round-start")
	hub.Lock()
	r := hub.Rooms[code]
	r.EndAt = hub.Now
	hub.Unlock()
	a.event(t, "live")
	b.event(t, "live")
	hub.Lock()
	for _, bot := range r.Players {
		if bot.Bot {
			bot.Alive = false
			bot.RespawnAt = 0
		}
	}
	p, q := r.Players[id], r.Players[bid]
	p.Motion = game.Motion{Pos: game.Vec{X: 0, Y: .01, Z: 0}, Ground: true}
	q.Motion = game.Motion{Pos: game.Vec{X: 0, Y: .01, Z: -10}, Ground: true}
	p.ProtectedUntil = 0
	q.ProtectedUntil = 0
	p.FireAt = 0
	r.ScoreLimit = 1
	hub.Unlock()
	// The browser's compact movement format must work through actual QUIC.
	data := make([]byte, 25)
	binary.LittleEndian.PutUint16(data, 0x4748)
	data[2], data[3] = 1, 1
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], math.Float32bits(1))
	if err := a.sess.SendDatagram(data); err != nil {
		t.Fatal(err)
	}
	packetCtx, packetCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer packetCancel()
	received := false
	for !received {
		packet, err := a.sess.ReceiveDatagram(packetCtx)
		if err != nil {
			t.Fatal("real QUIC datagram", err)
		}
		if len(packet) != 24+10*64 {
			continue
		}
		for i := 0; i < 10; i++ {
			off := 24 + i*64
			if binary.LittleEndian.Uint32(packet[off:]) == id && binary.LittleEndian.Uint32(packet[off+4:]) == 1 {
				if x := math.Float32frombits(binary.LittleEndian.Uint32(packet[off+8:])); x > 0 {
					received = true
				}
			}
		}
	}
	// Stop movement, aim at the other player, and confirm damage/kill over reliable QUIC streams.
	hub.Lock()
	p.Motion = game.Motion{Pos: game.Vec{}, Ground: true}
	p.Input = game.Input{}
	p.FireAt = 0
	hub.Unlock()
	a.send(t, game.Command{Type: "shot"})
	damage := b.event(t, "damage")
	if damage["id"] != float64(bid) {
		t.Fatal("damage delivered to wrong player")
	}
	time.Sleep(110 * time.Millisecond)
	a.send(t, game.Command{Type: "shot"})
	end := b.event(t, "round-end")
	if end["winner"] != float64(0) {
		t.Fatal("wrong winner", end)
	}
	a.close()
	restored := dial(game.Hello{Type: "hello", Room: code, Resume: resume})
	defer restored.close()
	wr := restored.event(t, "welcome")
	if wr["id"] != float64(id) {
		t.Fatal("resume changed identity")
	}
	// Cross-origin sessions cannot use room credentials.
	bad := &webtransport.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer bad.Close()
	badCtx, badCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer badCancel()
	resp, session, err := bad.Dial(badCtx, origin+"/connect", http.Header{"Origin": []string{"https://unrelated.example"}})
	if err == nil || session != nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatal("origin check not enforced", err)
	}
	restored.send(t, game.Command{Type: "leave"})
	restored.event(t, "left")
	hub.Lock()
	removed := hub.Rooms[code].Players[id] == nil
	hub.Unlock()
	if !removed {
		t.Fatal("acknowledged leave retained the player")
	}
	t.Logf("real HTTP/3: room %s, two players, input/snapshot datagrams, server hit/kill, winner, resume and origin rejection verified", code)
}
