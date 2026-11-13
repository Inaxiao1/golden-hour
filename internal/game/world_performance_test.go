package game

import (
	"math"
	"math/rand"
	"testing"
)

func TestBroadphaseMatchesFullHotelScan(t *testing.T) {
	w := Hotel()
	rng := rand.New(rand.NewSource(731))
	rays := []struct {
		o, d  Vec
		limit float64
	}{
		{Vec{-24, 1.62, 4}, Vec{1, 0, 0}, 60},
		{Vec{0, 20, 0}, Vec{0, -1, 0}, 50},
		{Vec{-4, 1, -4}, Vec{0, 0, 1}, 100},
		{Vec{4, 1, 4}, Vec{-1, 0, 0}, 0},
	}
	for i := 0; i < 3000; i++ {
		d := Vec{rng.Float64()*2 - 1, rng.Float64()*2 - 1, rng.Float64()*2 - 1}
		d = d.Mul(1 / math.Sqrt(d.Dot(d)))
		rays = append(rays, struct {
			o, d  Vec
			limit float64
		}{Vec{rng.Float64()*100 - 50, rng.Float64()*16 - 3, rng.Float64()*100 - 50}, d, rng.Float64() * 100})
	}
	for i, r := range rays {
		want := r.limit
		for _, c := range w.Colliders {
			if !c.NoRay {
				if n := rayBox(r.o, r.d, c.Min, c.Max, want); n >= 0 && n < want {
					want = n
				}
			}
		}
		if got := w.Ray(r.o, r.d, r.limit); math.Abs(got-want) > 1e-9 {
			t.Fatalf("ray %d: grid %v full scan %v", i, got, want)
		}
	}
	for i := 0; i < 3000; i++ {
		p := Vec{rng.Float64()*64 - 32, rng.Float64()*8 - 1, rng.Float64()*52 - 26}
		h := 1.2 + rng.Float64()*.6
		want := false
		for _, c := range w.Colliders {
			if !c.NoMove && p.X+.32 > c.Min[0]+.001 && p.X-.32 < c.Max[0]-.001 && p.Z+.32 > c.Min[2]+.001 && p.Z-.32 < c.Max[2]-.001 && p.Y+h > c.Min[1]+.002 && p.Y < c.Max[1]-.002 {
				want = true
				break
			}
		}
		if got := w.blocked(p, h); got != want {
			t.Fatalf("collision %d at %+v: grid %v full scan %v", i, p, got, want)
		}
	}
}

func TestQueryStampWrapAndDeduplication(t *testing.T) {
	w := &World{Colliders: []Collider{{Min: [3]float64{-8, 0, -8}, Max: [3]float64{8, 2, 8}}}}
	w.Build()
	w.seen[0] = 1
	w.queryID = ^uint32(0)
	count := 0
	w.each(0, 0, 5, func(Collider) { count++ })
	if count != 1 {
		t.Fatalf("collider visited %d times after stamp wrap", count)
	}
	w.queryID = ^uint32(0)
	if got := w.Ray(Vec{-10, 1, 0}, Vec{1, 0, 0}, 20); got != 2 {
		t.Fatalf("ray lost collider after wrap: %v", got)
	}
}
