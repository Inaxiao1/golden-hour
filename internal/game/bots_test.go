package game

import (
	"math"
	"testing"
	"time"
)

func seats(t *testing.T, r *Room, humans int) {
	t.Helper()
	teams, bots := [2]int{}, 0
	for _, p := range r.Players {
		teams[p.Team]++
		if p.Bot {
			bots++
			if !p.Ready {
				t.Fatal("bot not ready")
			}
		}
	}
	if teams != [2]int{5, 5} || bots != 10-humans || humanCount(r) != humans {
		t.Fatalf("invalid seats: teams=%v bots=%d humans=%d", teams, bots, humanCount(r))
	}
	if r.Players[r.Host] == nil || r.Players[r.Host].Bot {
		t.Fatal("bot became host")
	}
}
func TestBotsFillReplaceSwitchLeaveAndCleanup(t *testing.T) {
	h := NewHub(floorWorld())
	a, err := h.Join(Hello{Create: true}, &testPeer{})
	if err != nil {
		t.Fatal(err)
	}
	r := h.Rooms[a.Room]
	seats(t, r, 1)
	h.Handle(a, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	if r.Phase != "countdown" {
		t.Fatal("solo host could not deploy with bots")
	}
	h.Tick(h.Started.Add(4 * time.Second))
	if r.Phase != "live" {
		t.Fatal("solo round did not start")
	}
	b, err := h.Join(Hello{Room: r.ID}, &testPeer{})
	if err != nil {
		t.Fatal(err)
	}
	seats(t, r, 2)
	if !r.Players[b.ID].Alive {
		t.Fatal("late human did not spawn")
	}
	h.Leave(b)
	seats(t, r, 1)
	h.end(r)
	h.Handle(a, Command{Type: "team", Team: 1})
	seats(t, r, 1)
	if r.Players[a.ID].Team != 1 {
		t.Fatal("filler blocked team switch")
	}
	h.Disconnect(a)
	h.Tick(h.Started.Add(5 * time.Second))
	if r.Players[a.ID].AI == nil && r.Phase == "live" {
		t.Fatal("disconnected human not controlled by AI")
	}
	h.Tick(time.Now().Add(21 * time.Second))
	if len(h.Rooms) != 0 {
		t.Fatal("bots kept an empty room alive")
	}
}
func TestBotsFightReloadAndRespawn(t *testing.T) {
	h := NewHub(floorWorld())
	a, _ := h.Join(Hello{Create: true}, &testPeer{})
	r := h.Rooms[a.Room]
	h.Handle(a, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	r.ScoreLimit = 1000
	var bot *Player
	for _, p := range r.Players {
		if p.Bot && p.Team == 1 {
			bot = p
			break
		}
	}
	human := r.Players[a.ID]
	h.Tick(h.Started.Add(4 * time.Second))
	// Isolate one enemy bot and a stationary human to prove real weapon authority.
	for _, p := range r.Players {
		p.Alive = false
		p.RespawnAt = 0
	}
	human.Alive = true
	human.HP = 100
	human.Motion = Motion{Pos: Vec{0, 0, -10}, Ground: true}
	human.ProtectedUntil = 0
	bot.Alive = true
	bot.HP = 100
	bot.Motion = Motion{Ground: true}
	bot.Weapon = 0
	bot.Ammo[0] = 0
	bot.FireAt = 0
	bot.ProtectedUntil = 0
	for tick := 241; tick < 241+60*12; tick++ {
		h.Tick(h.Started.Add(time.Duration(tick) * time.Second / 60))
	}
	if human.Deaths == 0 || bot.Kills == 0 || r.Scores[1] == 0 {
		t.Fatalf("bot failed to reload/fire/kill: hp=%v deaths=%d kills=%d", human.HP, human.Deaths, bot.Kills)
	}
	h.kill(r, bot, human, false)
	at := h.Now
	for i := 1; i <= 181; i++ {
		h.Tick(h.Started.Add(time.Duration((at + float64(i)/60) * float64(time.Second))))
	}
	if !bot.Alive || bot.HP != 100 {
		t.Fatal("bot did not respawn")
	}
}
func TestBotCannotSeeOrShootThroughWalls(t *testing.T) {
	h := NewHub(floorWorld())
	a, _ := h.Join(Hello{Create: true}, &testPeer{})
	r := h.Rooms[a.Room]
	h.World.Colliders = append(h.World.Colliders, Collider{Min: [3]float64{-100, 0, -6}, Max: [3]float64{100, 10, -5}})
	h.World.Build()
	r.Phase = "live"
	r.EndAt = 100
	p := r.Players[a.ID]
	p.Alive = true
	p.HP = 100
	p.Motion.Pos = Vec{0, 0, -10}
	var bot *Player
	for _, q := range r.Players {
		if q.Bot && q.Team == 1 {
			bot = q
			break
		}
	}
	bot.Alive = true
	bot.HP = 100
	bot.Ammo[0] = 30
	bot.Weapon = 0
	for tick := 0; tick < 120; tick++ {
		h.Tick(h.Started.Add(time.Duration(tick) * time.Second / 60))
	}
	if bot.Ammo[0] != 30 || p.HP != 100 {
		t.Fatal("bot fired through opaque cover")
	}
}
func TestHotelBotNavigationAndCombat(t *testing.T) {
	w := Hotel()
	if len(w.Nodes) < 100 {
		t.Fatal("missing hotel navigation")
	}
	route := w.route(Spawns[0].Pos, Spawns[7].Pos)
	if len(route) < 3 {
		t.Fatal("no route across hotel", route)
	}
	h := NewHub(w)
	a, _ := h.Join(Hello{Create: true}, &testPeer{})
	r := h.Rooms[a.Room]
	h.Handle(a, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	r.ScoreLimit = 1000
	initial := map[uint32]Vec{}
	for id, p := range r.Players {
		initial[id] = p.Motion.Pos
	}
	for tick := 1; tick <= 60*50; tick++ {
		h.Tick(h.Started.Add(time.Duration(tick) * time.Second / 60))
	}
	moved := 0
	botKills := 0
	for id, p := range r.Players {
		if p.Bot {
			if math.Hypot(p.Motion.Pos.X-initial[id].X, p.Motion.Pos.Z-initial[id].Z) > 3 {
				moved++
			}
			botKills += p.Kills
		}
	}
	if moved < 5 || botKills == 0 {
		t.Fatalf("hotel bots inactive: moved=%d kills=%d scores=%v", moved, botKills, r.Scores)
	}
	t.Logf("hotel: %d bots moved, %d bot kills, score %v", moved, botKills, r.Scores)
}

func TestDisconnectedSeatAutopilotAndResume(t *testing.T) {
	h := NewHub(floorWorld())
	a, _ := h.Join(Hello{Create: true}, &testPeer{})
	r := h.Rooms[a.Room]
	h.Handle(a, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	h.Tick(h.Started.Add(4 * time.Second))
	p := r.Players[a.ID]
	token := p.Token
	h.Disconnect(a)
	h.Tick(h.Started.Add(5 * time.Second))
	if p.AI == nil || p.Peer != nil || p.Bot || len(r.Players) != 10 {
		t.Fatal("disconnected human seat was not retained under AI control")
	}
	restored, err := h.Join(Hello{Room: r.ID, Resume: token}, &testPeer{})
	if err != nil || restored.ID != a.ID || p.AI != nil || p.Input.Forward != 0 || p.Input.Right != 0 {
		t.Fatal("resume did not hand control back to the human", err)
	}
	h.Disconnect(restored)
	h.Tick(time.Now().Add(21 * time.Second))
	if len(h.Rooms) != 0 {
		t.Fatal("expired human kept bot room alive")
	}
}
