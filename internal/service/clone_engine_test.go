package service

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/audio"
	"voice/internal/clones"
	"voice/internal/core"
	"voice/internal/preset"
)

// A voice switches engines and back keeping its words, and its words can be
// cleared where the engine works without them, never where it needs them.
func TestCloneSetEngineAndTranscript(t *testing.T) {
	old := clones.Dir
	clones.Dir = t.TempDir()
	t.Cleanup(func() { clones.Dir = old })
	pcm := make([]float32, 24000*4)
	for i := range pcm {
		pcm[i] = float32(0.3 * math.Sin(float64(i)*2*math.Pi*220/24000))
	}
	clip := filepath.Join(t.TempDir(), "clip.wav")
	if err := os.WriteFile(clip, audio.EncodeWAV(pcm, 24000), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := clones.Add("anna", clip, "hello darling", "qwen3_fast", "test", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Config: cfg, Agents: &Agents{}}
	ctx := context.Background()

	if _, err := s.Clone(ctx, CloneArgs{Action: "set_engine", Name: "anna", Engine: "kokoro"}); err == nil {
		t.Fatal("a voice moved to an engine that does not clone")
	}
	if _, err := s.Clone(ctx, CloneArgs{Action: "set_transcript", Name: "anna"}); err == nil {
		t.Fatal("qwen3_fast needs the words, yet they were cleared")
	}
	if _, err := s.Clone(ctx, CloneArgs{Action: "set_engine", Name: "anna", Engine: "voxcpm2"}); err != nil {
		t.Fatal(err)
	}
	if r, err := cfg.Resolve(core.ClonePresetName("anna")); err != nil || r.Engine != "voxcpm2" {
		t.Fatalf("the preset after the switch: %+v %v", r, err)
	}
	res, err := s.Clone(ctx, CloneArgs{Action: "set_transcript", Name: "anna"})
	if err != nil || !strings.Contains(res.(string), "cleared") {
		t.Fatalf("clearing the words on voxcpm2: %v %v", res, err)
	}
	if p, _ := clones.Get("anna"); p.Text != "" || p.Engine != "voxcpm2" {
		t.Fatalf("after clearing: %+v", p)
	}
	// Back to qwen3_fast: without speech recognition the words cannot be
	// written down, so it says so.
	res, err = s.Clone(ctx, CloneArgs{Action: "set_engine", Name: "anna", Engine: "qwen3_fast"})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]any); m["warning"] == nil {
		t.Fatalf("no warning about the missing words: %v", m)
	}
}
