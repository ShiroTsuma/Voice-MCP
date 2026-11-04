package overlay

import "testing"

func TestTintAndInnerRing(t *testing.T) {
	// One white pixel, half transparent, premultiplied.
	px := []byte{128, 128, 128, 128}
	got := tinted(px, 0xFF0000)
	if got[3] != 128 || got[2] <= got[1] || got[2] <= got[0] {
		t.Fatalf("tinted to red: %v", got)
	}
	if got[2] > got[3] {
		t.Fatal("not premultiplied any more")
	}
	if z := tinted([]byte{0, 0, 0, 0}, 0xFF0000); z[3] != 0 {
		t.Fatal("a clear pixel got colour")
	}

	// A 4x4 clear canvas with a 4x4 opaque image half its size in the middle.
	const side = 4
	small := make([]byte, side*side*4)
	for i := range small {
		small[i] = 255
	}
	out := within(make([]byte, side*side*4), small, side, 0.5)
	if out[(1*side+1)*4+3] != 255 || out[3] != 0 {
		t.Fatalf("inner ring: middle %d, corner %d", out[(1*side+1)*4+3], out[3])
	}
}
