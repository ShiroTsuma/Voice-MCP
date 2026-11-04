package clones

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/audio"
)

func tone(seconds float64, rate int) []float32 {
	out := make([]float32, int(seconds*float64(rate)))
	for i := range out {
		out[i] = float32(0.3 * math.Sin(2*math.Pi*220*float64(i)/float64(rate)))
	}
	return out
}

func clip(t *testing.T, silence, speech float64) string {
	rate := 16000
	pcm := append(make([]float32, int(silence*float64(rate))), tone(speech, rate)...)
	pcm = append(pcm, make([]float32, int(silence*float64(rate)))...)
	path := filepath.Join(t.TempDir(), "clip.wav")
	os.WriteFile(path, audio.EncodeWAV(pcm, rate), 0o644)
	return path
}

func TestAddTrimsAndCaps(t *testing.T) {
	Dir = t.TempDir()
	p, err := Add("test", clip(t, 2, 20), "hello there", "", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Duration > maxSpeech+0.5 || p.Duration < maxSpeech-2.5 {
		t.Errorf("duration %.1f, want about %.0f", p.Duration, maxSpeech)
	}
	if p.Text != "hello there" || p.Engine != "pocket" {
		t.Errorf("profile %+v", p)
	}
	b, _ := os.ReadFile(p.Ref)
	pcm, rate, err := audio.DecodeWAV(b)
	if err != nil || rate != refRate {
		t.Fatalf("ref: rate %d err %v", rate, err)
	}
	// Leading silence trimmed to the 150 ms margin.
	lead := 0
	for lead < len(pcm) && math.Abs(float64(pcm[lead])) < 0.01 {
		lead++
	}
	if float64(lead)/refRate > 0.3 {
		t.Errorf("leading silence %.2f s", float64(lead)/refRate)
	}
	if _, err := Add("test", clip(t, 0, 5), "", "", "", false); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("duplicate accepted: %v", err)
	}
	if len(List()) != 1 || Remove("test") != nil || len(List()) != 0 {
		t.Error("list/remove")
	}
}

func TestAddRejectsShortAndBadNames(t *testing.T) {
	Dir = t.TempDir()
	if _, err := Add("short", clip(t, 1, 1), "", "", "", false); err == nil {
		t.Error("1 s clip accepted")
	}
	if _, err := Add("../evil", clip(t, 0, 5), "", "", "", false); err == nil {
		t.Error("path name accepted")
	}
}

func TestCheckJudgesRecordings(t *testing.T) {
	rate := 16000
	speech := append(make([]float32, rate), tone(5, rate)...)
	if p, w := Check(speech, rate); len(p)+len(w) != 0 {
		t.Errorf("clean clip: %v %v", p, w)
	}
	if p, _ := Check(make([]float32, 3*rate), rate); len(p) == 0 {
		t.Error("silence passed")
	}
	clipped := tone(5, rate)
	for i := range clipped {
		clipped[i] *= 4
		clipped[i] = max(-1, min(1, clipped[i]))
	}
	if p, _ := Check(clipped, rate); len(p) == 0 || !strings.Contains(p[0], "clipped") {
		t.Errorf("clipped clip: %v", p)
	}
	noisy := append(make([]float32, rate), tone(5, rate)...)
	for i := range noisy {
		noisy[i] += float32(0.08 * math.Sin(float64(i)*12.9898))
	}
	if _, w := Check(noisy, rate); len(w) == 0 {
		t.Error("noisy clip passed without a warning")
	}
}
