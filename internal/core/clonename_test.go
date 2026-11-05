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
	if _, err := clones.Add("nova", clip, "hello", "kokoro", "test", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultPreset = "clone-nova"
	cfg.Characters = []preset.Character{{Name: "Nova", Preset: "clone-nova"}, {Name: "Clara", Preset: "brisk"}}
	cfg.Presets["tuned-nova"] = preset.Preset{Extends: "clone-nova"}
	RegisterClones(cfg)
	if cfg.DefaultPreset != "nova" || cfg.Characters[0].Preset != "nova" || cfg.Characters[1].Preset != "brisk" || cfg.Presets["tuned-nova"].Extends != "nova" {
		t.Fatalf("not migrated: %q %+v %q", cfg.DefaultPreset, cfg.Characters, cfg.Presets["tuned-nova"].Extends)
	}
	for _, n := range []string{"nova", "clone-nova", "tuned-nova"} {
		if r, err := cfg.Resolve(n); err != nil || r.Name == "" {
			t.Errorf("%s: %+v %v", n, r, err)
		}
	}
	if !ClonePresetTaken(cfg, "brisk") || ClonePresetTaken(cfg, "nova") || ClonePresetTaken(cfg, "nobody") {
		t.Error("taken names wrong")
	}
}
