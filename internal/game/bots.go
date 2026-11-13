package game

import "math"

// Filler bots occupy real seats and use the same movement, weapons and damage
// rules as human players. Disconnected seats use AI during the reconnect grace.
type BotState struct {
	Target                               uint32
	ThinkAt, RouteAt, FireAfter, StuckAt float64
	Path                                 []int
	LastPos                              Vec
	Seed                                 uint32
	AimYaw, AimPitch, Strafe             float64
}

func humanCount(r *Room) int {
	n := 0
	for _, p := range r.Players {
		if !p.Bot {
			n++
		}
	}
	return n
}

func (h *Hub) fillBots(r *Room) {
	if humanCount(r) == 0 {
		return
	}
	for team := 0; team < 2; team++ {
		n := 0
		for _, p := range r.Players {
			if p.Team == team {
				n++
			}
		}
		for id, p := range r.Players {
			if n <= 5 {
				break
			}
			if p.Bot && p.Team == team {
				delete(r.Players, id)
				n--
			}
		}
		for n < 5 {
			h.nextID++
			names := []string{"隼", "回声", "夜枭", "北极星", "游隼", "猎风", "霜刃", "黑曜", "赤狐", "微光"}
			name := ""
			for offset := range names {
				candidate := "人机 · " + names[(int(h.nextID)+offset)%len(names)]
				used := false
				for _, q := range r.Players {
					if q.Bot && q.Name == candidate {
						used = true
						break
					}
				}
				if !used {
					name = candidate
					break
				}
			}
			p := &Player{ID: h.nextID, Name: name, Team: team, Bot: true, Ready: true, HP: 100, Weapon: int(h.nextID) % 3}
			r.Players[p.ID] = p
			if r.Phase == "live" || r.Phase == "countdown" {
				h.spawn(r, p)
			}
			n++
		}
	}
}

func (a *BotState) random() float64 {
	a.Seed ^= a.Seed << 13
	a.Seed ^= a.Seed >> 17
	a.Seed ^= a.Seed << 5
	return float64(a.Seed) / float64(math.MaxUint32)
}

func (w *World) visible(from, to Vec) bool {
	d := to.Sub(from)
	length := math.Sqrt(d.Dot(d))
	return length < .01 || w.Ray(from, d.Mul(1/length), length) >= length-.05
}

func (w *World) nearestNode(pos Vec) int {
	best, distance := -1, math.MaxFloat64
	for i, n := range w.Nodes {
		d := n.Vec().Sub(pos)
		cost := d.Dot(d)
		if cost < distance && math.Abs(d.Y) < 1.2 && w.visible(pos.Add(Vec{0, .8, 0}), n.Vec().Add(Vec{0, .8, 0})) {
			best, distance = i, cost
		}
	}
	return best
}

// Dijkstra runs only when a route changes, not on every simulation tick.
func (w *World) route(from, to Vec) []int {
	start, goal := w.nearestNode(from), w.nearestNode(to)
	if start < 0 || goal < 0 {
		return nil
	}
	dist, prev, used := make([]float64, len(w.Nodes)), make([]int, len(w.Nodes)), make([]bool, len(w.Nodes))
	for i := range dist {
		dist[i], prev[i] = math.MaxFloat64, -1
	}
	dist[start] = 0
	for range w.Nodes {
		u := -1
		for i := range dist {
			if !used[i] && (u < 0 || dist[i] < dist[u]) {
				u = i
			}
		}
		if u < 0 || dist[u] == math.MaxFloat64 {
			break
		}
		if u == goal {
			break
		}
		used[u] = true
		for _, v := range w.Nodes[u].Links {
			if v < 0 || v >= len(w.Nodes) {
				continue
			}
			d := w.Nodes[u].Vec().Sub(w.Nodes[v].Vec())
			cost := dist[u] + math.Sqrt(d.Dot(d))
			if cost < dist[v] {
				dist[v], prev[v] = cost, u
			}
		}
	}
	if start != goal && prev[goal] < 0 {
		return nil
	}
	path := []int{goal}
	for path[len(path)-1] != start {
		path = append(path, prev[path[len(path)-1]])
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (h *Hub) botInput(r *Room, p *Player) {
	if p.AI == nil {
		p.AI = &BotState{Seed: p.ID*747796405 + 2891336453, LastPos: p.Motion.Pos, StuckAt: h.Now, FireAfter: h.Now + .6}
	}
	a := p.AI
	eye := p.Motion.Pos.Add(Vec{0, 1.62, 0})
	if h.Now >= a.ThinkAt {
		a.ThinkAt = h.Now + .22
		best, cost := uint32(0), math.MaxFloat64
		for _, q := range r.Players {
			if q.Team == p.Team || !q.Alive {
				continue
			}
			d := q.Motion.Pos.Sub(p.Motion.Pos)
			v := d.Dot(d)
			if !h.World.visible(eye, q.Motion.Pos.Add(Vec{0, 1.35, 0})) {
				v += 10000
			}
			if v < cost || (v == cost && q.ID < best) {
				best, cost = q.ID, v
			}
		}
		if a.Target != best {
			a.Target = best
			a.FireAfter = h.Now + .35 + a.random()*.35
			a.RouteAt = 0
		}
		a.AimYaw = (a.random() - .5) * .055
		a.AimPitch = (a.random() - .5) * .035
		a.Strafe = (a.random() - .5) * .9
	}
	q := r.Players[a.Target]
	p.Input = Input{Yaw: p.Yaw}
	if q == nil || !q.Alive {
		return
	}
	target := q.Motion.Pos.Add(Vec{0, 1.35, 0})
	d := target.Sub(eye)
	distance := math.Sqrt(d.Dot(d))
	visible := distance < 65 && h.World.visible(eye, target)
	goal := q.Motion.Pos
	if !visible || distance > 18 {
		if h.Now >= a.RouteAt {
			a.RouteAt = h.Now + 1.1 + a.random()*.4
			a.Path = h.World.route(p.Motion.Pos, q.Motion.Pos)
		}
		for len(a.Path) > 0 {
			n := h.World.Nodes[a.Path[0]].Vec()
			if math.Hypot(n.X-p.Motion.Pos.X, n.Z-p.Motion.Pos.Z) < .8 && math.Abs(n.Y-p.Motion.Pos.Y) < .8 {
				a.Path = a.Path[1:]
			} else {
				goal = n
				break
			}
		}
	}
	move := goal.Sub(p.Motion.Pos)
	length := math.Hypot(move.X, move.Z)
	if visible {
		p.Yaw = math.Atan2(-d.X, -d.Z) + a.AimYaw
		p.Pitch = math.Atan2(d.Y, math.Hypot(d.X, d.Z)) + a.AimPitch
	} else if length > .05 {
		p.Yaw = math.Atan2(-move.X, -move.Z)
		p.Pitch = 0
	}
	p.Input.Yaw, p.Input.Pitch = p.Yaw, p.Pitch
	if length > .5 && (!visible || distance > 12) {
		x, z := move.X/length, move.Z/length
		p.Input.Forward = -x*math.Sin(p.Yaw) - z*math.Cos(p.Yaw)
		p.Input.Right = x*math.Cos(p.Yaw) - z*math.Sin(p.Yaw)
		if !visible {
			p.Input.Buttons |= ButtonSprint
		}
	}
	if visible {
		p.Input.Right = clamp(p.Input.Right+a.Strafe, -1, 1)
		p.Input.Buttons |= ButtonADS
		if h.Now >= a.FireAfter {
			if p.Ammo[p.Weapon] == 0 && p.ReloadAt == 0 {
				// Bots can resupply between magazines, but must finish normal reloads.
				p.Reserve[p.Weapon] = max(p.Reserve[p.Weapon], Weapons[p.Weapon].Mag)
				p.ReloadAt = h.Now + Weapons[p.Weapon].Reload
				p.FireAt = p.ReloadAt
				h.broadcast(r, Event{"type": "reload", "id": p.ID, "duration": Weapons[p.Weapon].Reload})
			} else {
				h.shoot(r, p, Command{Tick: h.TickID, Yaw: p.Yaw, Pitch: p.Pitch})
			}
		}
	}
	if h.Now-a.StuckAt > 1 {
		if math.Hypot(p.Motion.Pos.X-a.LastPos.X, p.Motion.Pos.Z-a.LastPos.Z) < .45 && length > 2 {
			p.Input.Buttons |= ButtonJump
			p.Input.Right = 1
			a.RouteAt = 0
		}
		a.LastPos = p.Motion.Pos
		a.StuckAt = h.Now
	}
}
