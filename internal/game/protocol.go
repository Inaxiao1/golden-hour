package game

import (
	"encoding/binary"
	"math"
	"sort"
)

const SnapshotHeader = 24
const SnapshotPlayer = 64

// Binary snapshots fit ten players in 664 bytes, below QUIC's 1200-byte MTU.
func (h *Hub) snapshot(r *Room) []byte {
	ids := make([]int, 0, len(r.Players))
	for id := range r.Players {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	b := make([]byte, SnapshotHeader+len(ids)*SnapshotPlayer)
	u16 := func(i int, v int) { binary.LittleEndian.PutUint16(b[i:], uint16(clamp(float64(v), 0, 65535))) }
	u32 := func(i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
	f32 := func(i int, v float64) { u32(i, math.Float32bits(float32(v))) }
	u16(0, 0x4748)
	b[2] = 1
	b[3] = 2
	u32(4, h.TickID)
	u32(8, uint32(h.Now*1000))
	b[12] = map[string]byte{"lobby": 0, "countdown": 1, "live": 2, "over": 3}[r.Phase]
	b[13] = byte(len(ids))
	u16(14, r.Scores[0])
	u16(16, r.Scores[1])
	u32(18, uint32(math.Max(0, r.EndAt-h.Now)*1000))
	b[22] = byte(int8(r.Winner))
	for i, id := range ids {
		p := r.Players[uint32(id)]
		off := SnapshotHeader + i*SnapshotPlayer
		u32(off, p.ID)
		u32(off+4, p.Ack)
		for j, v := range []float64{p.Motion.Pos.X, p.Motion.Pos.Y, p.Motion.Pos.Z, p.Motion.Vel.X, p.Motion.Vel.Y, p.Motion.Vel.Z, p.Yaw, p.Pitch} {
			f32(off+8+j*4, v)
		}
		u16(off+40, int(math.Ceil(p.HP)))
		u16(off+42, p.Ammo[p.Weapon])
		u16(off+44, p.Reserve[p.Weapon])
		u16(off+46, p.Kills)
		u16(off+48, p.Deaths)
		u16(off+50, p.Score)
		u16(off+52, int(math.Max(0, p.RespawnAt-h.Now)*1000))
		u16(off+54, int(math.Max(0, p.ReloadAt-h.Now)*1000))
		b[off+56] = byte(p.Team)
		if p.Alive {
			b[off+57] |= 1
		}
		if p.Motion.Crouch {
			b[off+57] |= 2
		}
		if p.Motion.Ground {
			b[off+57] |= 4
		}
		if p.Peer != nil || p.Bot || !p.Disconnected.IsZero() {
			b[off+57] |= 8
		}
		if h.Now < p.ProtectedUntil {
			b[off+57] |= 16
		}
		if p.Motion.JumpHeld {
			b[off+57] |= 32
		}
		if p.Bot {
			b[off+57] |= 64
		}
		b[off+58] = byte(p.Weapon)
		u16(off+60, int(math.Max(0, p.FireAt-h.Now)*1000))
	}
	return b
}
