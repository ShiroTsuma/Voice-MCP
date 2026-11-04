package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/clones"
	"voice/internal/engine"
	"voice/internal/preset"
)

func TestRemovedVoiceFallsBackToDefault(t *testing.T) {
	clones.Dir = t.TempDir()
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetPreset("ghost", preset.Preset{Engine: "sapi", Voice: preset.Voice{Voice: engine.Voice{Name: clonePrefix + "tomek"}}})
	cfg.SetPreset("moved", preset.Preset{Engine: "sapi", Voice: preset.Voice{Voice: engine.Voice{Ref: filepath.Join(t.TempDir(), "gone.wav")}}})

	for _, name := range []string{"ghost", "moved"} {
		p, err := cfg.Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		got, note, err := voiceOrDefault(cfg, p, "en")
		if err != nil || got.Name != cfg.DefaultPreset || !strings.Contains(note, name) {
			t.Fatalf("%s: preset %q note %q err %v", name, got.Name, note, err)
		}
	}
	if !strings.Contains(func() string { p, _ := cfg.Resolve("ghost"); _, n, _ := voiceOrDefault(cfg, p, "en"); return n }(), "cloned voice tomek") {
		t.Fatal("note does not name the clone")
	}

	// A preset whose voice exists is left alone.
	p, _ := cfg.Resolve(cfg.DefaultPreset)
	if got, note, err := voiceOrDefault(cfg, p, "en"); err != nil || note != "" || got.Name != p.Name {
		t.Fatalf("default: %q %q %v", got.Name, note, err)
	}

	// The default itself without its voice has nothing to fall back to.
	cfg.DefaultPreset = "ghost"
	p, _ = cfg.Resolve("ghost")
	if _, _, err := voiceOrDefault(cfg, p, "en"); !errors.Is(err, ErrVoiceGone) || !errors.Is(err, clones.ErrNotFound) {
		t.Fatalf("default without its voice: %v", err)
	}
}
