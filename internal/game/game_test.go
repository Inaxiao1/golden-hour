package game

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"
	"time"
)

type testPeer struct {
	mu     sync.Mutex
	events []Event
	data   []byte
	closed bool
}

func (p *testPeer) Send(e Event) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
	return true
}
func (p *testPeer) Snapshot(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data = append([]byte(nil), b...)
}
func (p *testPeer) Close(string) { p.mu.Lock(); defer p.mu.Unlock(); p.closed = true }
func floorWorld() *World {
	w := &World{Colliders: []Collider{{Min: [3]float64{-100, -1, -100}, Max: [3]float64{100, 0, 100}}}}
	w.Build()
	return w
}
func pair(t *testing.T) (*Hub, Binding, Binding, *Room) {
	t.Helper()
	h := NewHub(floorWorld())
	a, err := h.Join(Hello{Create: true, Name: "Alice"}, &testPeer{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Join(Hello{Room: a.Room, Name: "Bob"}, &testPeer{})
	if err != nil {
		t.Fatal(err)
	}
	return h, a, b, h.Rooms[a.Room]
}
func live(h *Hub, r *Room) {
	for id, p := range r.Players {
		if p.Bot {
			delete(r.Players, id)
		}
	}
	r.Phase = "live"
	r.EndAt = 500
	h.Now = 10
	for _, p := range r.Players {
		h.spawn(r, p)
		p.ProtectedUntil = 0
		p.FireAt = 0
	}
}
func TestRoomAuthorityAndLifecycle(t *testing.T) {
	h, a, b, r := pair(t)
	h.Handle(b, Command{Type: "start"})
	if r.Phase != "lobby" {
		t.Fatal("non-host started round")
	}
	h.Handle(a, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	if r.Phase != "lobby" {
		t.Fatal("unready opponent accepted")
	}
	h.Handle(b, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	if r.Phase != "countdown" {
		t.Fatal("ready room did not start")
	}
	h.Tick(h.Started.Add(4 * time.Second))
	if r.Phase != "live" {
		t.Fatal("countdown did not transition")
	}
	h.Handle(b, Command{Type: "team", Team: 0})
	if r.Players[b.ID].Team != 1 {
		t.Fatal("mid-round team switch accepted")
	}
	h.Disconnect(a)
	if r.Host != b.ID {
		t.Fatal("host did not migrate")
	}
	p := r.Players[a.ID]
	binding, err := h.Join(Hello{Room: r.ID, Resume: p.Token}, &testPeer{})
	if err != nil || binding.ID != a.ID {
		t.Fatal("resume failed", err)
	}
	h.Disconnect(a)
	if p.Peer == nil {
		t.Fatal("stale connection disconnected resumed player")
	}
	if _, err = h.Join(Hello{Room: r.ID, Resume: "invalid"}, &testPeer{}); err == nil {
		t.Fatal("invalid resume accepted")
	}
	h.Disconnect(binding)
	p.Disconnected = time.Now().Add(-21 * time.Second)
	h.Tick(time.Now())
	if r.Players[a.ID] != nil {
		t.Fatal("expired player retained")
	}
}
func TestInputNeverTrustsPositionOrClientRate(t *testing.T) {
	h, a, _, r := pair(t)
	live(h, r)
	p := r.Players[a.ID]
	p.Motion = Motion{Pos: Vec{0, .01, 0}, Ground: true}
	h.ApplyInput(a, Input{Type: "input", Seq: 1, Forward: 100, Right: 100, Yaw: 0})
	if p.Input.Forward != 1 || p.Input.Right != 1 {
		t.Fatal("intent not clamped")
	}
	h.ApplyInput(a, Input{Type: "input", Seq: 2, Yaw: math.NaN()})
	if p.Ack != 1 {
		t.Fatal("NaN accepted")
	}
	h.ApplyInput(a, Input{Type: "input", Seq: 5000, Forward: 1})
	if p.Ack != 1 {
		t.Fatal("sequence jump accepted")
	}
	for i := 2; i < 1000; i++ {
		h.ApplyInput(a, Input{Type: "input", Seq: uint32(i), Forward: 1})
	}
	h.Tick(h.Started.Add(10 * time.Second))
	if math.Hypot(p.Motion.Pos.X, p.Motion.Pos.Z) > .13 {
		t.Fatal("packet flood advanced movement faster than server tick")
	}
}
func TestShootingOcclusionFireRateAndScore(t *testing.T) {
	h, a, b, r := pair(t)
	live(h, r)
	p, q := r.Players[a.ID], r.Players[b.ID]
	p.Motion.Pos = Vec{0, 0, 0}
	q.Motion.Pos = Vec{0, 0, -10}
	h.World.Colliders = append(h.World.Colliders, Collider{Min: [3]float64{-2, 0, -6}, Max: [3]float64{2, 3, -5}})
	h.World.Build()
	shot := Command{Type: "shot", Yaw: 0, Pitch: 0}
	h.Handle(a, shot)
	if q.HP != 100 {
		t.Fatal("shot penetrated solid wall")
	}
	ammo := p.Ammo[0]
	h.Handle(a, shot)
	if p.Ammo[0] != ammo {
		t.Fatal("fire rate not enforced")
	}
	h.World = floorWorld()
	r.ScoreLimit = 1
	p.FireAt = 0
	h.Now += 1
	h.Handle(a, shot)
	if q.HP >= 100 {
		t.Fatal("unobstructed shot missed")
	}
	p.FireAt = 0
	h.Now += 1
	h.Handle(a, shot)
	if q.Alive || q.HP != 0 || p.Kills != 1 || q.Deaths != 1 || r.Scores[0] != 1 || r.Phase != "over" || r.Winner != 0 {
		t.Fatalf("invalid kill/round state: hp=%v alive=%v score=%v phase=%s", q.HP, q.Alive, r.Scores, r.Phase)
	}
	before := p.Ammo[0]
	h.Handle(a, shot)
	if p.Ammo[0] != before {
		t.Fatal("shot accepted after round ended")
	}
}
func TestFriendlyFireProtectionReloadRespawn(t *testing.T) {
	h, a, b, r := pair(t)
	live(h, r)
	p, q := r.Players[a.ID], r.Players[b.ID]
	p.Motion.Pos = Vec{}
	q.Motion.Pos = Vec{0, 0, -10}
	q.Team = p.Team
	h.Handle(a, Command{Type: "shot"})
	if q.HP != 100 {
		t.Fatal("friendly fire")
	}
	q.Team = 1
	q.ProtectedUntil = 100
	p.FireAt = 0
	h.Handle(a, Command{Type: "shot"})
	if q.HP != 100 {
		t.Fatal("spawn protection failed")
	}
	p.Ammo[0] = 0
	p.FireAt = 0
	h.Handle(a, Command{Type: "reload"})
	if p.ReloadAt == 0 {
		t.Fatal("reload did not start")
	}
	h.Handle(a, Command{Type: "shot"})
	if p.Ammo[0] != 0 {
		t.Fatal("shot during reload")
	}
	h.Tick(h.Started.Add(13 * time.Second))
	if p.Ammo[0] != Weapons[0].Mag || p.Reserve[0] != Weapons[0].Reserve-Weapons[0].Mag {
		t.Fatal("authoritative reload failed")
	}
	h.kill(r, q, p, false)
	h.Tick(h.Started.Add(17 * time.Second))
	if !q.Alive || q.HP != 100 || q.Deaths != 1 {
		t.Fatal("respawn failed")
	}
}
func TestCapacityAndSnapshotBudget(t *testing.T) {
	h, a, _, r := pair(t)
	for i := 0; i < 8; i++ {
		if _, err := h.Join(Hello{Room: a.Room}, &testPeer{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Join(Hello{Room: a.Room}, &testPeer{}); err == nil {
		t.Fatal("capacity exceeded")
	}
	b := h.snapshot(r)
	if len(b) != 664 || len(b) > 1200 || binary.LittleEndian.Uint16(b) != 0x4748 || b[13] != 10 {
		t.Fatal("invalid snapshot envelope", len(b))
	}
}
func TestHotelSpawnsAndCollision(t *testing.T) {
	w := Hotel()
	if len(w.Colliders) < 700 {
		t.Fatal("hotel fixture incomplete")
	}
	for _, s := range Spawns {
		if w.blocked(s.Pos.Add(Vec{0, .015, 0}), 1.8) {
			t.Errorf("spawn inside collision: %+v", s)
		}
	}
}
func TestPredictionConformance(t *testing.T) {
	w := floorWorld()
	m := Motion{Pos: Vec{0, .01, 0}, Ground: true}
	for i := 0; i < 120; i++ {
		w.Move(&m, Input{Forward: 1})
	}
	if math.Abs(m.Pos.Z-(-8.943333333333333)) > 1e-9 || m.Pos.Y != 0 {
		t.Fatalf("prediction changed: %+v", m)
	}
	w.Colliders = append(w.Colliders, Collider{Min: [3]float64{-2, 0, -4}, Max: [3]float64{2, 3, -3}})
	w.Build()
	m = Motion{Pos: Vec{0, .01, 0}, Ground: true}
	for i := 0; i < 120; i++ {
		w.Move(&m, Input{Forward: 1})
	}
	if m.Pos.Z < -2.681 {
		t.Fatal("walked through wall", m.Pos)
	}
}
