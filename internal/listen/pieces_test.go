package listen

import "testing"

// A long recording reaches the recognizer in pieces Whisper hears whole,
// cut between speech segments, and nothing is dropped.
func TestToPieces(t *testing.T) {
	sec := func(n float64) []float32 { return make([]float32, int(n*rate)) }
	segs := [][]float32{sec(10), sec(10), sec(10), sec(20), sec(4), sec(60)}
	pieces := toPieces(segs)
	total, gaps := 0, 0
	for i, p := range pieces {
		if len(p) > maxPiece {
			t.Fatalf("piece %d is %.1f s", i, float64(len(p))/rate)
		}
		total += len(p)
	}
	for _, s := range segs {
		gaps -= len(s)
	}
	gaps += total
	if gaps < 0 || gaps%(rate/5) != 0 {
		t.Fatalf("audio lost or added: %d samples beyond the speech", gaps)
	}
	// 10+10 | 10 | 20+4 | 25 | 25 | 10
	if len(pieces) != 6 {
		t.Fatalf("%d pieces, want 6", len(pieces))
	}
}
