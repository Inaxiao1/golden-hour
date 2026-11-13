package transport

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LankiYang/golden-hour/internal/game"
	"github.com/pion/webrtc/v4"
)

type rtcTestClient struct {
	pc              *webrtc.PeerConnection
	reliable, state *webrtc.DataChannel
	events          chan map[string]any
	packets         chan []byte
}

// Real ICE/DTLS/SCTP, including the same HTTP offer exchange used by browsers.
func dialRTC(t *testing.T, httpClient *http.Client, origin string, hello game.Hello) *rtcTestClient {
	t.Helper()
	settings := webrtc.SettingEngine{}
	settings.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	c := &rtcTestClient{pc: pc, events: make(chan map[string]any, 128), packets: make(chan []byte, 128)}
	t.Cleanup(c.close)
	ordered, unordered, retransmits := true, false, uint16(0)
	protocol := "gh-pvp-v1"
	c.reliable, err = pc.CreateDataChannel("reliable", &webrtc.DataChannelInit{Ordered: &ordered, Protocol: &protocol})
	if err != nil {
		t.Fatal(err)
	}
	c.state, err = pc.CreateDataChannel("state", &webrtc.DataChannelInit{Ordered: &unordered, MaxRetransmits: &retransmits, Protocol: &protocol})
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{}, 2)
	for _, dc := range []*webrtc.DataChannel{c.reliable, c.state} {
		dc.OnOpen(func() { opened <- struct{}{} })
	}
	c.reliable.OnMessage(func(m webrtc.DataChannelMessage) {
		var e map[string]any
		if json.Unmarshal(m.Data, &e) == nil {
			select {
			case c.events <- e:
			default:
			}
		}
	})
	c.state.OnMessage(func(m webrtc.DataChannelMessage) {
		select {
		case c.packets <- m.Data:
		default:
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		t.Fatal("client ICE gathering timeout")
	}
	body, _ := json.Marshal(pc.LocalDescription())
	req, _ := http.NewRequest(http.MethodPost, origin+"/api/rtc/offer", bytes.NewReader(body))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("offer: %s %s", resp.Status, data)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatal("SDP answer content type")
	}
	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	if err := pc.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-opened:
		case <-time.After(4 * time.Second):
			t.Fatal("data channel did not open")
		}
	}
	c.send(t, hello)
	return c
}
func (c *rtcTestClient) close() { c.pc.Close() }
func (c *rtcTestClient) send(t *testing.T, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.reliable.SendText(string(b) + "\n"); err != nil {
		t.Fatal(err)
	}
}
func (c *rtcTestClient) event(t *testing.T, kind string) map[string]any {
	t.Helper()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e := <-c.events:
			if e["type"] == kind {
				return e
			}
		case <-timer.C:
			t.Fatalf("waiting for WebRTC %s", kind)
			return nil
		}
	}
}
func (c *rtcTestClient) ack(t *testing.T, id, seq uint32) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case packet := <-c.packets:
			if len(packet) != 664 || packet[2] != 1 || packet[3] != 2 {
				continue
			}
			for i := 0; i < 10; i++ {
				off := 24 + i*64
				if binary.LittleEndian.Uint32(packet[off:]) == id && binary.LittleEndian.Uint32(packet[off+4:]) == seq {
					return
				}
			}
		case <-timer.C:
			t.Fatal("WebRTC binary input was not acknowledged in a snapshot")
			return
		}
	}
}

func testRTCOnly(t *testing.T, httpClient *http.Client, origin string, hub *game.Hub) {
	c := dialRTC(t, httpClient, origin, game.Hello{Type: "hello", Create: true, Name: "RTC solo"})
	defer c.close()
	welcome := c.event(t, "welcome")
	code, id := welcome["room"].(string), uint32(welcome["id"].(float64))
	hub.Lock()
	bots := 0
	for _, p := range hub.Rooms[code].Players {
		if p.Bot {
			bots++
		}
	}
	hub.Unlock()
	if bots != 9 {
		t.Fatalf("RTC solo room has %d bots, want 9", bots)
	}
	c.send(t, game.Command{Type: "ready", Ready: true})
	c.send(t, game.Command{Type: "start"})
	c.event(t, "round-start")
	input := make([]byte, 25)
	binary.LittleEndian.PutUint16(input, 0x4748)
	input[2], input[3] = 1, 1
	binary.LittleEndian.PutUint32(input[4:], 1)
	if err := c.state.Send(input); err != nil {
		t.Fatal(err)
	}
	c.ack(t, id, 1)
	c.send(t, game.Command{Type: "leave"})
	c.event(t, "left")
	hub.Lock()
	_, exists := hub.Rooms[code]
	hub.Unlock()
	if exists {
		t.Fatal("RTC leave retained an empty room")
	}
}

func testRTCOfferRejections(t *testing.T, httpClient *http.Client, origin string, server *Server) {
	waitRTCEmpty(t, server)
	invalid := dialRTC(t, httpClient, origin, game.Hello{Type: "hello", Room: "00000000"})
	if e := invalid.event(t, "error"); e["message"] == "" {
		t.Fatal("missing room rejection")
	}
	invalid.close()
	waitRTCEmpty(t, server)
	for _, tc := range []struct {
		name, method, requestOrigin, contentType, body string
		status                                         int
	}{
		{"origin", "POST", "https://unrelated.example", "application/json", `{}`, 403},
		{"missing origin", "POST", "", "application/json", `{}`, 403},
		{"method", "GET", origin, "application/json", `{}`, 405},
		{"type", "POST", origin, "text/plain", `{}`, 415},
		{"json", "POST", origin, "application/json", `{`, 400},
		{"SDP", "POST", origin, "application/json", `{"type":"offer","sdp":"invalid"}`, 400},
		{"size", "POST", origin, "application/json", strings.Repeat(" ", 65537), 400},
	} {
		t.Run("RTC rejects "+tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, origin+"/api/rtc/offer", strings.NewReader(tc.body))
			req.Header.Set("Origin", tc.requestOrigin)
			req.Header.Set("Content-Type", tc.contentType)
			resp, err := httpClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("got %d want %d", resp.StatusCode, tc.status)
			}
		})
	}
	// No rejected SDP negotiation may retain a server peer/slot.
	waitRTCEmpty(t, server)
}

func waitRTCEmpty(t *testing.T, server *Server) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); ; {
		server.rtc.mu.Lock()
		count := len(server.rtc.peers)
		server.rtc.mu.Unlock()
		if count == 0 && server.sessions.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("RTC negotiation leaked peers: %d, total sessions: %d", count, server.sessions.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
