package game

import (
	"testing"
	"time"
)

func BenchmarkHotelRay(b *testing.B) {
	w := Hotel()
	o := Vec{-24, 1.62, 4}
	d := Vec{1, 0, 0}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Ray(o, d, 60)
	}
}
func BenchmarkHotelMove(b *testing.B) {
	w := Hotel()
	m := Motion{Pos: Spawns[0].Pos, Ground: true}
	in := Input{Forward: 1, Yaw: -1.57}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Move(&m, in)
	}
}
func BenchmarkHotelRoomTick(b *testing.B) {
	h := NewHub(Hotel())
	a, _ := h.Join(Hello{Create: true}, &benchPeer{})
	r := h.Rooms[a.Room]
	h.Handle(a, Command{Type: "ready", Ready: true})
	h.Handle(a, Command{Type: "start"})
	r.ScoreLimit = int(^uint(0) >> 1)
	r.TimeLimit = 1e12
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Tick(h.Started.Add(time.Duration(i+241) * time.Second / 60))
	}
}
func BenchmarkHotelRoute(b *testing.B) {
	w := Hotel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.route(Spawns[0].Pos, Spawns[7].Pos)
	}
}

type benchPeer struct{}

func (*benchPeer) Send(Event) bool { return true }
func (*benchPeer) Snapshot([]byte) {}
func (*benchPeer) Close(string)    {}
