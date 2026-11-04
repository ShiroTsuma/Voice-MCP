package overlay

import "testing"

func TestScaledKeepsOpaqueCentreAndClearCorners(t *testing.T) {
	const side = 8
	px := make([]byte, side*side*4)
	for y := 2; y < 6; y++ {
		for x := 2; x < 6; x++ {
			i := (y*side + x) * 4
			px[i], px[i+1], px[i+2], px[i+3] = 200, 100, 50, 255
		}
	}
	out := scaled(px, side, 12)
	if len(out) != 12*12*4 {
		t.Fatalf("size %d", len(out))
	}
	if a := out[(6*12+6)*4+3]; a != 255 {
		t.Errorf("centre alpha %d", a)
	}
	if a := out[3]; a != 0 {
		t.Errorf("corner alpha %d", a)
	}
	if &scaled(px, side, side)[0] != &px[0] {
		t.Error("same size copied")
	}
}
