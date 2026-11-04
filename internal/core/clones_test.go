package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/audio"
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

func TestPromptStandsForTheRecording(t *testing.T) {
	clones.Dir = t.TempDir()
	dir := t.TempDir()
	wav := filepath.Join(dir, "clip.wav")
	pcm := make([]float32, 24000*3)
	for i := range pcm {
		pcm[i] = 0.3 * float32(i%50) / 50
	}
	os.WriteFile(wav, audio.EncodeWAV(pcm, 24000), 0o644)
	if _, err := clones.Add("luna", wav, "Hello there.", "qwen3", "consent", false); err != nil {
		t.Fatal(err)
	}
	v := engine.Voice{Name: clonePrefix + "luna"}
	if got, err := resolveVoice(v); err != nil || got.Prompt != "" {
		t.Fatalf("without a prompt: %+v %v", got, err)
	}
	os.WriteFile(clones.PromptFile("luna"), []byte("x"), 0o644)
	clones.SetPrompt("luna", "qwen3", "m", "r")
	p, _ := clones.Get("luna")
	os.Rename(p.Ref, p.Ref+".away") // the recording is not there for a while
	got, err := resolveVoice(v)
	if err != nil || got.Prompt != clones.PromptFile("luna") || got.PromptEngine != "qwen3" || got.PromptModel != "m" {
		t.Fatalf("prompt without the WAV: %+v %v", got, err)
	}
	os.Remove(clones.PromptFile("luna")) // neither prompt nor recording: gone
	if _, err := resolveVoice(v); !errors.Is(err, ErrVoiceGone) {
		t.Fatalf("nothing left: %v", err)
	}
}
