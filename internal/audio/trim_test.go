package audio

import (
	"math"
	"testing"
)

func TestTrimSilence(t *testing.T) {
	const rate = 1000
	tone := func(n int) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = float32(0.5 * math.Sin(float64(i)))
		}
		return out
	}
	// 0.8 s quiet, 0.5 s of sound, 1.2 s quiet.
	pcm := append(append(make([]float32, 800), tone(500)...), make([]float32, 1200)...)
	got := TrimSilence(pcm, rate, 0.05, 0.2)
	// Kept: 50 ms before, the sound, 200 ms after (to the 10 ms window).
	if len(got) < 740 || len(got) > 770 {
		t.Fatalf("kept %d samples, want about 750", len(got))
	}
	if math.Abs(float64(got[49])) > 0.001 || math.Abs(float64(got[60])) < 0.001 && math.Abs(float64(got[61])) < 0.001 {
		t.Fatalf("lead is wrong: %v", got[45:65])
	}
	// Already short pads stay; quiet alone stays as it was.
	short := append(append(make([]float32, 20), tone(300)...), make([]float32, 30)...)
	if len(TrimSilence(short, rate, 0.05, 0.2)) != len(short) {
		t.Fatal("short pads were cut")
	}
	if quiet := make([]float32, 500); len(TrimSilence(quiet, rate, 0.05, 0.2)) != 500 {
		t.Fatal("all-quiet audio changed")
	}
}
