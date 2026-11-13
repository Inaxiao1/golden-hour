package game

import (
	_ "embed"
	"encoding/json"
	"math"
)

const TickRate = 60
const Step = 1.0 / TickRate

type Vec struct{ X, Y, Z float64 }

func (a Vec) Add(b Vec) Vec         { return Vec{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec) Sub(b Vec) Vec         { return Vec{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec) Mul(n float64) Vec     { return Vec{a.X * n, a.Y * n, a.Z * n} }
func (a Vec) Dot(b Vec) float64     { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func clamp(x, a, b float64) float64 { return math.Max(a, math.Min(b, x)) }
func finite(x float64) bool         { return !math.IsNaN(x) && !math.IsInf(x, 0) }

type Collider struct {
	Min    [3]float64 `json:"min"`
	Max    [3]float64 `json:"max"`
	NoMove bool       `json:"noMove"`
	NoRay  bool       `json:"noRay"`
}
type NavNode struct {
	Pos   [3]float64 `json:"pos"`
	Links []int      `json:"links"`
}

func (n NavNode) Vec() Vec { return Vec{n.Pos[0], n.Pos[1], n.Pos[2]} }

type World struct {
	Nodes     []NavNode  `json:"nodes"`
	Colliders []Collider `json:"colliders"`
	grid      map[[2]int][]int
	seen      []uint32
	queryID   uint32
}

// Queries reuse scratch stamps. World access is serialized by the Hub mutex;
// callbacks passed to each must not issue a nested World query.

//go:embed maps/hotel.json
var hotelJSON []byte

func Hotel() *World {
	w := new(World)
	if err := json.Unmarshal(hotelJSON, w); err != nil {
		panic(err)
	}
	w.Build()
	return w
}
func (w *World) Build() {
	w.grid = make(map[[2]int][]int)
	for i, c := range w.Colliders {
		for x := int(math.Floor(c.Min[0] / 4)); x <= int(math.Floor(c.Max[0]/4)); x++ {
			for z := int(math.Floor(c.Min[2] / 4)); z <= int(math.Floor(c.Max[2]/4)); z++ {
				k := [2]int{x, z}
				w.grid[k] = append(w.grid[k], i)
			}
		}
	}
	w.seen = make([]uint32, len(w.Colliders))
	w.queryID = 0
}
func (w *World) each(x, z, r float64, fn func(Collider)) {
	w.queryID++
	if w.queryID == 0 {
		clear(w.seen)
		w.queryID = 1
	}
	for i := int(math.Floor((x - r) / 4)); i <= int(math.Floor((x+r)/4)); i++ {
		for j := int(math.Floor((z - r) / 4)); j <= int(math.Floor((z+r)/4)); j++ {
			for _, n := range w.grid[[2]int{i, j}] {
				if w.seen[n] != w.queryID {
					w.seen[n] = w.queryID
					fn(w.Colliders[n])
				}
			}
		}
	}
}
func (w *World) blocked(p Vec, h float64) bool {
	hit := false
	w.each(p.X, p.Z, .33, func(c Collider) {
		if !c.NoMove && p.X+.32 > c.Min[0]+.001 && p.X-.32 < c.Max[0]-.001 && p.Z+.32 > c.Min[2]+.001 && p.Z-.32 < c.Max[2]-.001 && p.Y+h > c.Min[1]+.002 && p.Y < c.Max[1]-.002 {
			hit = true
		}
	})
	return hit
}

type Motion struct {
	Pos, Vel Vec
	Ground   bool
	Crouch   bool
	JumpHeld bool
}
type Input struct {
	Type    string  `json:"type"`
	Seq     uint32  `json:"seq"`
	Forward float64 `json:"forward"`
	Right   float64 `json:"right"`
	Yaw     float64 `json:"yaw"`
	Pitch   float64 `json:"pitch"`
	Buttons uint8   `json:"buttons"`
}

const (
	ButtonJump   = 1
	ButtonSprint = 2
	ButtonCrouch = 4
	ButtonADS    = 8
)

// Move is fixed-step and takes intent only. Clients never submit positions.
func (w *World) Move(m *Motion, in Input) {
	h := 1.8
	if in.Buttons&ButtonCrouch != 0 {
		m.Crouch = true
	} else if !w.blocked(m.Pos, 1.8) {
		m.Crouch = false
	}
	if m.Crouch {
		h = 1.2
	}
	speed := 4.6
	if in.Buttons&ButtonSprint != 0 && !m.Crouch && in.Buttons&ButtonADS == 0 {
		speed = 7.5
	}
	if m.Crouch {
		speed = 2.7
	}
	if in.Buttons&ButtonADS != 0 {
		speed *= .65
	}
	f, r := in.Forward, in.Right
	if length := math.Hypot(f, r); length > 1 {
		f /= length
		r /= length
	}
	sy, cy := math.Sin(in.Yaw), math.Cos(in.Yaw)
	tx, tz := (-sy*f+cy*r)*speed, (-cy*f-sy*r)*speed
	ax, az := tx-m.Vel.X, tz-m.Vel.Z
	if length := math.Hypot(ax, az); length > 36*Step {
		ax *= 36 * Step / length
		az *= 36 * Step / length
	}
	m.Vel.X += ax
	m.Vel.Z += az
	jump := in.Buttons&ButtonJump != 0
	if jump && !m.JumpHeld && m.Ground {
		m.Vel.Y = 7
		m.Ground = false
	}
	m.JumpHeld = jump
	m.Vel.Y -= 22 * Step
	for axis := 0; axis < 2; axis++ {
		n := m.Pos
		if axis == 0 {
			n.X += m.Vel.X * Step
		} else {
			n.Z += m.Vel.Z * Step
		}
		if !w.blocked(n, h) {
			m.Pos = n
		} else if m.Ground {
			n.Y += .42
			if !w.blocked(n, h) {
				m.Pos = n
			}
		}
	}
	dy := m.Vel.Y * Step
	oldY := m.Pos.Y
	m.Ground = false
	w.each(m.Pos.X, m.Pos.Z, .33, func(c Collider) {
		if c.NoMove || m.Pos.X+.32 <= c.Min[0]+.001 || m.Pos.X-.32 >= c.Max[0]-.001 || m.Pos.Z+.32 <= c.Min[2]+.001 || m.Pos.Z-.32 >= c.Max[2]-.001 {
			return
		}
		if dy <= 0 && oldY >= c.Max[1]-.02 && oldY+dy <= c.Max[1] {
			dy = math.Max(dy, c.Max[1]-oldY)
			m.Ground = true
		}
		if dy > 0 && oldY+h <= c.Min[1]+.002 && oldY+h+dy >= c.Min[1] {
			dy = math.Min(dy, c.Min[1]-h-oldY)
		}
	})
	m.Pos.Y += dy
	if m.Ground || math.Abs(dy-m.Vel.Y*Step) > .00001 {
		m.Vel.Y = 0
	}
	// Keep malformed/unstable simulations from leaving the finite hotel domain.
	m.Pos.X = clamp(m.Pos.X, -31, 31)
	m.Pos.Z = clamp(m.Pos.Z, -24, 25)
}
func rayBox(o, d Vec, min, max [3]float64, limit float64) float64 {
	near, far := 0.0, limit
	orig := [3]float64{o.X, o.Y, o.Z}
	dir := [3]float64{d.X, d.Y, d.Z}
	for i := 0; i < 3; i++ {
		if math.Abs(dir[i]) < 1e-9 {
			if orig[i] < min[i] || orig[i] > max[i] {
				return -1
			}
			continue
		}
		a, b := (min[i]-orig[i])/dir[i], (max[i]-orig[i])/dir[i]
		if a > b {
			a, b = b, a
		}
		near = math.Max(near, a)
		far = math.Min(far, b)
		if near > far {
			return -1
		}
	}
	if far < 0 {
		return -1
	}
	return near
}
func (w *World) Ray(o, d Vec, limit float64) float64 {
	// The hotel contains hundreds of colliders, but a ray only crosses a small
	// set of 4m broadphase cells. Query those cells instead of scanning the full
	// map for every bot sight check and every pellet.
	end := o.Add(d.Mul(limit))
	minX, maxX := math.Min(o.X, end.X), math.Max(o.X, end.X)
	minZ, maxZ := math.Min(o.Z, end.Z), math.Max(o.Z, end.Z)
	w.queryID++
	if w.queryID == 0 {
		clear(w.seen)
		w.queryID = 1
	}
	for x := int(math.Floor(minX / 4)); x <= int(math.Floor(maxX/4)); x++ {
		for z := int(math.Floor(minZ / 4)); z <= int(math.Floor(maxZ/4)); z++ {
			for _, i := range w.grid[[2]int{x, z}] {
				if w.seen[i] == w.queryID {
					continue
				}
				w.seen[i] = w.queryID
				c := w.Colliders[i]
				if !c.NoRay {
					if n := rayBox(o, d, c.Min, c.Max, limit); n >= 0 && n < limit {
						limit = n
					}
				}
			}
		}
	}
	return limit
}
func raySphere(o, d, center Vec, r float64) float64 {
	v := o.Sub(center)
	b := v.Dot(d)
	disc := b*b - v.Dot(v) + r*r
	if disc < 0 {
		return -1
	}
	n := -b - math.Sqrt(disc)
	if n < 0 {
		return -1
	}
	return n
}

type Spawn struct {
	Pos  Vec
	Yaw  float64
	Team int
}

var Spawns = []Spawn{
	{Vec{-24.5, 0, 4.25}, -1.57, 0}, {Vec{-22.5, 0, -2.5}, -1.57, 0}, {Vec{-24.5, 0, -16.5}, -1.2, 0},
	{Vec{-16.5, 0, -17.5}, 0, 0}, {Vec{-10.5, 0, -11.5}, -1.57, 0}, {Vec{-20, 0, 15.5}, -1.57, 0}, {Vec{-16, 0, 3}, -1.57, 0},
	{Vec{25, 0, -16.5}, 1.57, 1}, {Vec{20, 0, -11}, 1.57, 1}, {Vec{25.6, 0, -6.3}, 1.57, 1},
	{Vec{24.3, 0, 6.2}, 1.57, 1}, {Vec{14, 0, -2.5}, 3.14, 1}, {Vec{20, 0, 15.5}, 1.57, 1}, {Vec{10.5, 0, -18}, 1.57, 1},
}
