package audio

import (
	"math"
	"testing"
)

func TestWAVRoundTrip(t *testing.T) {
	in := []float32{0, 0.5, -0.5, 0.25}
	out, rate, err := DecodeWAV(EncodeWAV(in, 22050))
	if err != nil || rate != 22050 || len(out) != len(in) {
		t.Fatalf("decode: %v rate=%d len=%d", err, rate, len(out))
	}
	for i := range in {
		if math.Abs(float64(out[i]-in[i])) > 1e-3 {
			t.Errorf("sample %d: %v != %v", i, out[i], in[i])
		}
	}
}

func TestWAVStreamingSizes(t *testing.T) {
	b := EncodeWAV([]float32{0.1, 0.2, 0.3}, 16000)
	// espeak-ng --stdout leaves the sizes at 0xFFFFFFFF.
	for _, off := range []int{4, 40} {
		b[off], b[off+1], b[off+2], b[off+3] = 0xFF, 0xFF, 0xFF, 0xFF
	}
	out, _, err := DecodeWAV(b)
	if err != nil || len(out) != 3 {
		t.Fatalf("got %d samples, err %v", len(out), err)
	}
}

func TestResampleLength(t *testing.T) {
	in := make([]float32, 22050)
	for i := range in {
		in[i] = float32(math.Sin(float64(i) / 10))
	}
	out := Resample(in, 22050, DeviceRate)
	if len(out) != DeviceRate {
		t.Fatalf("len %d", len(out))
	}
}
