package core

import (
	"os"
	"path/filepath"
	"testing"

	"voice/internal/audio"
	"voice/internal/clones"
	"voice/internal/preset"
)

// A cloned voice's preset is its own name; settings that still say
// clone-<name> are read as the new name, and both resolve.
func TestCloneNamesWithoutPrefix(t *testing.T) {
	old := clones.Dir
	clones.Dir = t.TempDir()
	defer func() { clones.Dir = old }()
	clip := filepath.Join(t.TempDir(), "me.wav")
	pcm := make([]float32, 24000*5)
	for i := range pcm {
		pcm[i] = 0.3 * float32((i/40)%2*2-1) // a buzz loud enough to count as speech
	}
	os.WriteFile(clip, audio.EncodeWAV(pcm, 24000), 0o644)
	if _, err := clones.Add("luna", clip, "hello", "kokoro", "test", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultPreset = "clone-luna"
	cfg.Characters = []preset.Character{{Name: "Luna", Preset: "clone-luna"}, {Name: "Clara", Preset: "brisk"}}
	cfg.Presets["tuned-luna"] = preset.Preset{Extends: "clone-luna"}
	RegisterClones(cfg)
	if cfg.DefaultPreset != "luna" || cfg.Characters[0].Preset != "luna" || cfg.Characters[1].Preset != "brisk" || cfg.Presets["tuned-luna"].Extends != "luna" {
		t.Fatalf("not migrated: %q %+v %q", cfg.DefaultPreset, cfg.Characters, cfg.Presets["tuned-luna"].Extends)
	}
	for _, n := range []string{"luna", "clone-luna", "tuned-luna"} {
		if r, err := cfg.Resolve(n); err != nil || r.Name == "" {
			t.Errorf("%s: %+v %v", n, r, err)
		}
	}
	if !ClonePresetTaken(cfg, "brisk") || ClonePresetTaken(cfg, "luna") || ClonePresetTaken(cfg, "nobody") {
		t.Error("taken names wrong")
	}
}
