package transport

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

func TestBinaryInputWireFixture(t *testing.T) {
	packet, _ := hex.DecodeString("484701012a0000000000803f000000bf0000003f000080be0a")
	in, ok := decodeInput(packet)
	if !ok || in.Type != "input" || in.Seq != 42 || in.Forward != 1 || in.Right != -.5 || in.Yaw != .5 || in.Pitch != -.25 || in.Buttons != 10 {
		t.Fatalf("bad wire input: %+v %v", in, ok)
	}
	for n := 0; n < 25; n++ {
		if _, ok := decodeInput(packet[:n]); ok {
			t.Fatalf("accepted truncated packet %d", n)
		}
	}
	for _, offset := range []int{0, 2, 3} {
		bad := append([]byte(nil), packet...)
		bad[offset]++
		if _, ok := decodeInput(bad); ok {
			t.Fatalf("accepted invalid header at %d", offset)
		}
	}
	if _, ok := decodeInput(append(packet, 0)); ok {
		t.Fatal("accepted trailing bytes")
	}
	for _, offset := range []int{8, 12, 16, 20} {
		for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
			bad := append([]byte(nil), packet...)
			binary.LittleEndian.PutUint32(bad[offset:], math.Float32bits(value))
			if _, ok := decodeInput(bad); ok {
				t.Fatalf("accepted nonfinite input at %d", offset)
			}
		}
	}
}
