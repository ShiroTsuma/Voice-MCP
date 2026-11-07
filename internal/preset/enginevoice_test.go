package preset

import "testing"

// An engine's own voice, named engine/voice, resolves like a preset of its
// own, and the voices turned off (one, or a whole engine) are not offered.
func TestEngineVoices(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.Resolve("kokoro/af_bella")
	if err != nil || r.Engine != "kokoro" || r.Voice.Name != "af_bella" {
		t.Fatalf("kokoro/af_bella: %+v, %v", r, err)
	}
	if !cfg.HasPreset("supertonic/3") || cfg.HasPreset("nosuch/voice") || cfg.HasPreset("kokoro/") {
		t.Fatal("which engine voices exist")
	}
	// A tuning may extend one.
	speed := 1.2
	cfg.SetPreset("voice-bella", Preset{Extends: "kokoro/af_bella", Speed: &speed})
	if r, err := cfg.Resolve("voice-bella"); err != nil || r.Engine != "kokoro" || r.Speed != 1.2 {
		t.Fatalf("a tuning of an engine voice: %+v, %v", r, err)
	}
	cfg.VoicesOff = []string{"kokoro/af_bella", "supertonic/*"}
	if cfg.VoiceOn("kokoro/af_bella") || cfg.VoiceOn("supertonic/0") || !cfg.VoiceOn("kokoro/af_heart") {
		t.Fatal("voices turned off")
	}
}
