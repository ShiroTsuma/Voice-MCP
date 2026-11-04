package service

import (
	"testing"

	"voice/internal/preset"
)

func TestSpeakPresetFromVoice(t *testing.T) {
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Characters = []preset.Character{{Name: "Luna", Preset: "narrator"}, {Name: "Plain"}}
	s := &Service{Config: cfg, Agents: &Agents{}}
	for voice, want := range map[string]string{"luna": "narrator", "Plain": "", "calm-mature": "calm-mature"} {
		got, err := s.speakPreset(SpeakArgs{Voice: voice})
		if err != nil || got != want {
			t.Errorf("voice %q: %q %v, want %q", voice, got, err, want)
		}
	}
	if _, err := s.speakPreset(SpeakArgs{Voice: "nobody"}); err == nil {
		t.Fatal("an unknown voice was taken")
	}
	if got, err := s.speakPreset(SpeakArgs{}); err != nil || got != "" {
		t.Fatalf("no voice: %q %v", got, err)
	}
}
