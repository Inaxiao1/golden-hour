package game

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Event map[string]any
type Peer interface {
	Send(Event) bool
	Snapshot([]byte)
	Close(string)
}
type Hello struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Room   string `json:"room"`
	Resume string `json:"resume"`
	Create bool   `json:"create"`
}
type Command struct {
	Type   string  `json:"type"`
	Ready  bool    `json:"ready"`
	Team   int     `json:"team"`
	Weapon string  `json:"weapon"`
	Tick   uint32  `json:"tick"`
	Yaw    float64 `json:"yaw"`
	Pitch  float64 `json:"pitch"`
	Nonce  float64 `json:"nonce"`
}
type Weapon struct {
	ID       string  `json:"id"`
	Damage   float64 `json:"damage"`
	Head     float64 `json:"head"`
	Range    float64 `json:"range"`
	Interval float64 `json:"interval"`
	Reload   float64 `json:"reload"`
	Mag      int     `json:"mag"`
	Reserve  int     `json:"reserve"`
	Pellets  int     `json:"pellets"`
	Spread   float64 `json:"spread"`
}

var Weapons = []Weapon{
	{"ar", 32, 1.8, 140, 60.0 / 650, 2.1, 30, 150, 1, 0},
	{"smg", 23, 1.6, 100, 60.0 / 900, 1.7, 32, 160, 1, 0},
	{"shotgun", 15, 1.3, 45, .85, 2.4, 8, 40, 8, .045},
	{"pistol", 30, 1.8, 100, .19, 1.3, 15, 90, 1, 0},
}

type History struct {
	Tick          uint32
	Pos           Vec
	Crouch, Alive bool
}
type Player struct {
	ID                                                  uint32
	Name                                                string
	Team                                                int
	Token                                               string
	Peer                                                Peer
	Generation                                          uint64
	Ready, Alive, Bot                                   bool
	AI                                                  *BotState
	Motion                                              Motion
	Input                                               Input
	Ack                                                 uint32
	Yaw, Pitch                                          float64
	HP                                                  float64
	Kills, Deaths, Score                                int
	Weapon                                              int
	Ammo, Reserve                                       [4]int
	ReloadAt, FireAt, RespawnAt, ProtectedUntil, HurtAt float64
	LastInput, Disconnected                             time.Time
	History                                             []History
}
type Room struct {
	ID         string
	Host       uint32
	Players    map[uint32]*Player
	Phase      string
	Scores     [2]int
	Winner     int
	EndAt      float64
	TimeLimit  float64
	ScoreLimit int
	Created    time.Time
}
type Binding struct {
	Room       string
	ID         uint32
	Generation uint64
}
type Hub struct {
	sync.Mutex
	World    *World
	Rooms    map[string]*Room
	nextID   uint32
	TickID   uint32
	Started  time.Time
	Now      float64
	MaxRooms int
}

func NewHub(w *World) *Hub {
	return &Hub{World: w, Rooms: make(map[string]*Room), Started: time.Now(), MaxRooms: 64}
}
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func roomCode() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}
func cleanName(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 20 {
		r = r[:20]
	}
	out := make([]rune, 0, len(r))
	for _, c := range r {
		if !unicode.IsControl(c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "干员"
	}
	return string(out)
}
func (h *Hub) Join(hello Hello, peer Peer) (Binding, error) {
	h.Lock()
	defer h.Unlock()
	id := strings.ToUpper(strings.TrimSpace(hello.Room))
	room := h.Rooms[id]
	if hello.Resume != "" && room != nil {
		for _, p := range room.Players {
			if !p.Bot && p.Token == hello.Resume {
				old := p.Peer
				p.Generation++
				p.Peer = peer
				p.Disconnected = time.Time{}
				p.Input = Input{}
				p.AI = nil
				p.LastInput = time.Now()
				if old != nil {
					old.Close("已在另一客户端恢复会话")
				}
				h.welcome(room, p)
				h.broadcastRoom(room)
				return Binding{room.ID, p.ID, p.Generation}, nil
			}
		}
		return Binding{}, errors.New("会话已过期，请重新加入房间")
	}
	if hello.Create {
		if len(h.Rooms) >= h.MaxRooms {
			return Binding{}, errors.New("服务器房间已满")
		}
		for {
			id = roomCode()
			if h.Rooms[id] == nil {
				break
			}
		}
		room = &Room{ID: id, Players: make(map[uint32]*Player), Phase: "lobby", Winner: -1, Created: time.Now(), TimeLimit: 480, ScoreLimit: 40}
		h.Rooms[id] = room
	}
	if room == nil {
		return Binding{}, errors.New("房间不存在，请检查房间码")
	}
	if humanCount(room) >= 10 {
		return Binding{}, errors.New("房间已满（最多 10 人）")
	}
	counts := [2]int{}
	for _, p := range room.Players {
		if !p.Bot {
			counts[p.Team]++
		}
	}
	team := 0
	if counts[0] > counts[1] {
		team = 1
	}
	for id, q := range room.Players {
		if q.Bot && q.Team == team {
			delete(room.Players, id)
			break
		}
	}
	h.nextID++
	p := &Player{ID: h.nextID, Name: cleanName(hello.Name), Team: team, Token: randomToken(24), Peer: peer, Generation: 1, HP: 100, LastInput: time.Now()}
	room.Players[p.ID] = p
	if room.Host == 0 {
		room.Host = p.ID
	}
	if room.Phase == "live" || room.Phase == "countdown" {
		h.spawn(room, p)
	}
	h.fillBots(room)
	h.welcome(room, p)
	h.broadcastRoom(room)
	return Binding{id, p.ID, p.Generation}, nil
}
func (h *Hub) welcome(r *Room, p *Player) {
	p.Peer.Send(Event{"type": "welcome", "id": p.ID, "room": r.ID, "resume": p.Token, "inputSeq": p.Ack, "tickRate": TickRate, "snapshotRate": 30, "protocol": 1, "weapons": Weapons})
	p.Peer.Send(h.roomEvent(r))
}
func (h *Hub) roomEvent(r *Room) Event {
	players := make([]Event, 0, len(r.Players))
	ids := make([]int, 0, len(r.Players))
	for id := range r.Players {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		p := r.Players[uint32(id)]
		players = append(players, Event{"id": p.ID, "name": p.Name, "team": p.Team, "ready": p.Ready, "connected": p.Peer != nil || p.Bot, "bot": p.Bot, "kills": p.Kills, "deaths": p.Deaths, "score": p.Score})
	}
	return Event{"type": "room", "room": r.ID, "host": r.Host, "phase": r.Phase, "players": players, "scores": r.Scores, "scoreLimit": r.ScoreLimit, "timeLimit": r.TimeLimit, "winner": r.Winner}
}
func (h *Hub) broadcast(r *Room, e Event) {
	for _, p := range r.Players {
		if p.Peer != nil {
			p.Peer.Send(e)
		}
	}
}
func (h *Hub) broadcastRoom(r *Room) { h.broadcast(r, h.roomEvent(r)) }
func (h *Hub) bound(b Binding) (*Room, *Player) {
	r := h.Rooms[b.Room]
	if r == nil {
		return nil, nil
	}
	p := r.Players[b.ID]
	if p == nil || p.Generation != b.Generation || p.Peer == nil {
		return nil, nil
	}
	return r, p
}
func (h *Hub) Disconnect(b Binding) {
	h.Lock()
	defer h.Unlock()
	r, p := h.bound(b)
	if p == nil {
		return
	}
	p.Peer = nil
	p.Disconnected = time.Now()
	p.Input = Input{}
	h.migrate(r)
	h.broadcastRoom(r)
}
func (h *Hub) migrate(r *Room) {
	if p := r.Players[r.Host]; p != nil && p.Peer != nil {
		return
	}
	r.Host = 0
	for id, p := range r.Players {
		if p.Peer != nil && (r.Host == 0 || id < r.Host) {
			r.Host = id
		}
	}
}
func (h *Hub) Leave(b Binding) {
	h.Lock()
	defer h.Unlock()
	r, p := h.bound(b)
	if p == nil {
		return
	}
	delete(r.Players, p.ID)
	h.fillBots(r)
	h.migrate(r)
	h.broadcastRoom(r)
	if humanCount(r) == 0 {
		delete(h.Rooms, r.ID)
	}
}
func (h *Hub) ApplyInput(b Binding, in Input) {
	if in.Type != "input" || !finite(in.Forward) || !finite(in.Right) || !finite(in.Yaw) || !finite(in.Pitch) || in.Seq == 0 {
		return
	}
	h.Lock()
	defer h.Unlock()
	_, p := h.bound(b)
	if p == nil || in.Seq <= p.Ack || in.Seq-p.Ack > 1024 {
		return
	}
	in.Forward = clamp(in.Forward, -1, 1)
	in.Right = clamp(in.Right, -1, 1)
	in.Pitch = clamp(in.Pitch, -1.5, 1.5)
	in.Yaw = math.Remainder(in.Yaw, 2*math.Pi)
	in.Buttons &= 15
	p.Input = in
	p.Ack = in.Seq
	p.Yaw = in.Yaw
	p.Pitch = in.Pitch
	p.LastInput = time.Now()
}
func (h *Hub) Handle(b Binding, c Command) {
	h.Lock()
	defer h.Unlock()
	r, p := h.bound(b)
	if p == nil {
		return
	}
	fail := func(s string) { p.Peer.Send(Event{"type": "error", "message": s}) }
	switch c.Type {
	case "ready":
		if r.Phase == "lobby" || r.Phase == "over" {
			p.Ready = c.Ready
			h.broadcastRoom(r)
		}
	case "team":
		if r.Phase != "lobby" && r.Phase != "over" {
			fail("对局中不能换队")
			return
		}
		if c.Team < 0 || c.Team > 1 {
			return
		}
		n := 0
		for _, q := range r.Players {
			if !q.Bot && q.Team == c.Team {
				n++
			}
		}
		if c.Team != p.Team && n >= 5 {
			fail("该队伍已满")
			return
		}
		p.Team = c.Team
		p.Ready = false
		h.fillBots(r)
		h.broadcastRoom(r)
	case "start":
		if p.ID != r.Host {
			fail("只有房主可以开始对局")
			return
		}
		if r.Phase != "lobby" && r.Phase != "over" {
			return
		}
		for _, q := range r.Players {
			if q.Peer != nil {
				if !q.Ready {
					fail("请等待所有玩家准备")
					return
				}
			}
		}
		h.fillBots(r)
		r.Scores = [2]int{}
		r.Winner = -1
		r.Phase = "countdown"
		r.EndAt = h.Now + 3
		for _, q := range r.Players {
			q.Kills = 0
			q.Deaths = 0
			q.Score = 0
			q.Input = Input{}
			q.History = nil
			h.spawn(r, q)
		}
		h.broadcast(r, Event{"type": "round-start", "countdown": 3})
		h.broadcastRoom(r)
	case "equip":
		for i, w := range Weapons {
			if w.ID == c.Weapon && i != p.Weapon {
				p.Weapon = i
				p.ReloadAt = 0
				p.FireAt = math.Max(p.FireAt, h.Now+.35)
				p.Peer.Send(Event{"type": "equipped", "weapon": w.ID})
				return
			}
		}
	case "reload":
		w := Weapons[p.Weapon]
		if p.Alive && r.Phase == "live" && p.ReloadAt == 0 && p.Ammo[p.Weapon] < w.Mag && p.Reserve[p.Weapon] > 0 {
			p.ReloadAt = h.Now + w.Reload
			p.FireAt = p.ReloadAt
			h.broadcast(r, Event{"type": "reload", "id": p.ID, "duration": w.Reload})
		}
	case "shot":
		h.shoot(r, p, c)
	case "ping":
		if finite(c.Nonce) {
			p.Peer.Send(Event{"type": "pong", "nonce": c.Nonce, "tick": h.TickID})
		}
	default:
		fail("未知指令")
	}
}
func (h *Hub) spawn(r *Room, p *Player) {
	best := Spawns[0]
	value := -math.MaxFloat64
	for i, s := range Spawns {
		if s.Team != p.Team || h.World.blocked(s.Pos.Add(Vec{0, .015, 0}), 1.8) {
			continue
		}
		distance := 100.0
		for _, q := range r.Players {
			if q.ID != p.ID && q.Alive && q.Team != p.Team {
				distance = math.Min(distance, math.Hypot(q.Motion.Pos.X-s.Pos.X, q.Motion.Pos.Z-s.Pos.Z))
			}
		}
		score := distance + float64((int(h.TickID)+int(p.ID)+i)%7)*.1
		if score > value {
			value = score
			best = s
		}
	}
	p.Motion = Motion{Pos: best.Pos.Add(Vec{0, .01, 0}), Ground: true}
	p.Yaw = best.Yaw
	p.Pitch = 0
	p.Input = Input{Yaw: best.Yaw}
	p.HP = 100
	p.Alive = true
	p.ReloadAt = 0
	p.RespawnAt = 0
	p.ProtectedUntil = h.Now + 1
	p.FireAt = h.Now + .5
	p.HurtAt = h.Now
	p.AI = nil
	for i, w := range Weapons {
		p.Ammo[i] = w.Mag
		p.Reserve[i] = w.Reserve
	}
	h.broadcast(r, Event{"type": "spawn", "id": p.ID, "x": p.Motion.Pos.X, "y": p.Motion.Pos.Y, "z": p.Motion.Pos.Z, "yaw": p.Yaw})
}
func (h *Hub) Tick(now time.Time) {
	h.Lock()
	defer h.Unlock()
	h.Now = now.Sub(h.Started).Seconds()
	h.TickID++
	for id, r := range h.Rooms {
		changed := false
		for pid, p := range r.Players {
			if !p.Bot && p.Peer == nil && now.Sub(p.Disconnected) > 20*time.Second {
				delete(r.Players, pid)
				changed = true
			}
		}
		if humanCount(r) == 0 {
			delete(h.Rooms, id)
			continue
		}
		if changed {
			h.fillBots(r)
			h.migrate(r)
			h.broadcastRoom(r)
		}
		if r.Phase == "countdown" && h.Now >= r.EndAt {
			r.Phase = "live"
			r.EndAt = h.Now + r.TimeLimit
			for _, p := range r.Players {
				p.ProtectedUntil = h.Now + 1
			}
			h.broadcast(r, Event{"type": "live"})
			h.broadcastRoom(r)
		}
		if r.Phase == "live" {
			for _, p := range r.Players {
				if !p.Alive {
					if p.RespawnAt > 0 && h.Now >= p.RespawnAt {
						h.spawn(r, p)
					}
					continue
				}
				if p.Bot || p.Peer == nil {
					h.botInput(r, p)
				}
				in := p.Input
				if !p.Bot && p.Peer != nil && now.Sub(p.LastInput) > 250*time.Millisecond {
					in.Forward = 0
					in.Right = 0
					in.Buttons = 0
				}
				h.World.Move(&p.Motion, in)
				if p.Motion.Pos.Y < -7 {
					h.kill(r, p, nil, false)
					continue
				}
				if p.HP < 100 && h.Now-p.HurtAt > 5 {
					p.HP = math.Min(100, p.HP+22*Step)
				}
				if p.ReloadAt > 0 && h.Now >= p.ReloadAt {
					w := Weapons[p.Weapon]
					n := min(w.Mag-p.Ammo[p.Weapon], p.Reserve[p.Weapon])
					p.Ammo[p.Weapon] += n
					p.Reserve[p.Weapon] -= n
					p.ReloadAt = 0
				}
			}
			if h.Now >= r.EndAt {
				h.end(r)
			}
		}
		for _, p := range r.Players {
			p.History = append(p.History, History{h.TickID, p.Motion.Pos, p.Motion.Crouch, p.Alive})
			if len(p.History) > 16 {
				p.History = p.History[len(p.History)-16:]
			}
		}
		if h.TickID%2 == 0 {
			snapshot := h.snapshot(r)
			for _, p := range r.Players {
				if p.Peer != nil {
					p.Peer.Snapshot(snapshot)
				}
			}
		}
	}
}
func (h *Hub) shoot(r *Room, p *Player, c Command) {
	if r.Phase != "live" || !p.Alive || !finite(c.Yaw) || !finite(c.Pitch) || p.ReloadAt > 0 || h.Now+1e-6 < p.FireAt || p.Ammo[p.Weapon] <= 0 {
		return
	}
	w := Weapons[p.Weapon]
	p.Ammo[p.Weapon]--
	p.ProtectedUntil = h.Now
	p.FireAt = h.Now + w.Interval
	p.Yaw = math.Remainder(c.Yaw, math.Pi*2)
	p.Pitch = clamp(c.Pitch, -1.5, 1.5)
	eye := 1.62
	if p.Motion.Crouch {
		eye = 1.04
	}
	origin := p.Motion.Pos.Add(Vec{0, eye, 0})
	dir := Vec{-math.Sin(p.Yaw) * math.Cos(p.Pitch), math.Sin(p.Pitch), -math.Cos(p.Yaw) * math.Cos(p.Pitch)}
	h.broadcast(r, Event{"type": "shot", "id": p.ID, "weapon": w.ID, "x": origin.X, "y": origin.Y, "z": origin.Z, "dx": dir.X, "dy": dir.Y, "dz": dir.Z})
	// Rewind target hitboxes at most 200 ms; shooter origin always comes from the server.
	tick := c.Tick
	if tick > h.TickID {
		tick = h.TickID
	}
	if h.TickID-tick > 12 {
		tick = h.TickID - min(h.TickID, uint32(12))
	}
	for pellet := 0; pellet < w.Pellets; pellet++ {
		if r.Phase != "live" {
			break
		}
		d := dir
		if w.Spread > 0 {
			angle := float64(pellet) * 2.399963
			radius := w.Spread * math.Sqrt(float64(pellet+1)/float64(w.Pellets))
			d = d.Add(Vec{math.Cos(angle) * radius, math.Sin(angle) * radius, 0})
			d = d.Mul(1 / math.Sqrt(d.Dot(d)))
		}
		limit := h.World.Ray(origin, d, w.Range)
		var victim *Player
		head := false
		for _, q := range r.Players {
			if q.ID == p.ID || q.Team == p.Team || !q.Alive || h.Now < q.ProtectedUntil {
				continue
			}
			pos, crouch, alive := q.Motion.Pos, q.Motion.Crouch, q.Alive
			for i := len(q.History) - 1; i >= 0; i-- {
				if q.History[i].Tick <= tick {
					pos = q.History[i].Pos
					crouch = q.History[i].Crouch
					alive = q.History[i].Alive
					break
				}
			}
			if !alive {
				continue
			}
			height := 1.8
			if crouch {
				height = 1.2
			}
			if n := rayBox(origin, d, [3]float64{pos.X - .3, pos.Y + .15, pos.Z - .3}, [3]float64{pos.X + .3, pos.Y + height - .3, pos.Z + .3}, limit); n >= 0 && n < limit {
				limit = n
				victim = q
				head = false
			}
			if n := raySphere(origin, d, pos.Add(Vec{0, height - .17, 0}), .19); n >= 0 && n < limit {
				limit = n
				victim = q
				head = true
			}
		}
		if victim != nil {
			damage := w.Damage
			if head {
				damage *= w.Head
			}
			if limit > 25 {
				damage *= clamp(1-(limit-25)/150, .5, 1)
			}
			victim.HP = math.Max(0, victim.HP-damage)
			victim.HurtAt = h.Now
			h.broadcast(r, Event{"type": "damage", "id": victim.ID, "attacker": p.ID, "hp": victim.HP, "amount": damage, "head": head})
			if victim.HP <= 0 {
				h.kill(r, victim, p, head)
			}
		}
	}
}
func (h *Hub) kill(r *Room, victim, killer *Player, head bool) {
	victim.Alive = false
	victim.HP = 0
	victim.Deaths++
	victim.ReloadAt = 0
	victim.RespawnAt = h.Now + 3
	victim.Motion.Vel = Vec{}
	killerID := uint32(0)
	weapon := "fell"
	if killer != nil {
		killerID = killer.ID
		weapon = Weapons[killer.Weapon].ID
		killer.Kills++
		killer.Score += 100
		r.Scores[killer.Team]++
	} else {
		r.Scores[1-victim.Team]++
	}
	h.broadcast(r, Event{"type": "kill", "killer": killerID, "victim": victim.ID, "weapon": weapon, "head": head, "respawn": 3, "scores": r.Scores})
	if r.Scores[0] >= r.ScoreLimit || r.Scores[1] >= r.ScoreLimit {
		h.end(r)
	}
}
func (h *Hub) end(r *Room) {
	if r.Phase != "live" {
		return
	}
	r.Phase = "over"
	r.Winner = -1
	if r.Scores[0] > r.Scores[1] {
		r.Winner = 0
	} else if r.Scores[1] > r.Scores[0] {
		r.Winner = 1
	}
	for _, p := range r.Players {
		p.Ready = p.Bot
	}
	h.broadcast(r, Event{"type": "round-end", "winner": r.Winner, "scores": r.Scores})
	h.broadcastRoom(r)
}
func (h *Hub) Info() Event {
	h.Lock()
	defer h.Unlock()
	players, bots := 0, 0
	for _, r := range h.Rooms {
		for _, p := range r.Players {
			if p.Bot {
				bots++
			}
			if p.Peer != nil {
				players++
			}
		}
	}
	return Event{"status": "ok", "rooms": len(h.Rooms), "players": players, "tick": h.TickID, "tickRate": TickRate, "transport": "webtransport-http3"}
}
func (b Binding) String() string { return fmt.Sprintf("%s/%d", b.Room, b.ID) }
