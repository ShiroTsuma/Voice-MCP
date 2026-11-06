package preset_test

import (
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/preset"
	"voice/internal/settings"
)

// How much is said aloud: a character's own level, else the general one,
// else short; the persona stops calling speech short above short.
func TestAloudOf(t *testing.T) {
	cfg := &preset.Config{}
	if got := cfg.AloudOf(preset.Character{}); got != preset.AloudShort {
		t.Fatalf("default: %d", got)
	}
	most := preset.AloudMost
	cfg.Serve.CharacterAloud = &most
	if got := cfg.AloudOf(preset.Character{}); got != preset.AloudMost {
		t.Fatalf("general: %d", got)
	}
	if got := cfg.AloudOf(preset.Character{Aloud: preset.AloudNearlyAll}); got != preset.AloudNearlyAll {
		t.Fatalf("own: %d", got)
	}
	bad := 9
	cfg.Serve.CharacterAloud = &bad
	if got := cfg.AloudOf(preset.Character{Aloud: 7}); got != preset.DefaultAloud {
		t.Fatalf("out of range: %d", got)
	}

	ch := preset.Character{Name: "Annie", Traits: []string{"cheerful"}}
	if p := ch.Persona(preset.ImmersionVoice); !strings.Contains(p, "stays short") {
		t.Fatalf("short persona: %q", p)
	}
	for _, imm := range []int{preset.ImmersionLight, preset.ImmersionVoice} {
		if p := ch.PersonaAloud(imm, preset.AloudMost); strings.Contains(p, "short") {
			t.Fatalf("immersion %d, most aloud, still short: %q", imm, p)
		}
	}
	if ch.PersonaAloud(preset.ImmersionFull, preset.AloudNearlyAll) != ch.Persona(preset.ImmersionFull) {
		t.Fatal("full immersion already says as much as it would")
	}

	if preset.AloudNote(preset.AloudShort) != "" {
		t.Fatal("short needs no note")
	}
	for level, want := range map[int]string{preset.AloudMost: "substance aloud", preset.AloudNearlyAll: "nearly all of each reply aloud"} {
		n := preset.AloudNote(level)
		if !strings.Contains(n, want) || !strings.Contains(n, "same turn") || !strings.Contains(n, "stay on screen") {
			t.Fatalf("note %d: %q", level, n)
		}
		if c := preset.AloudChanged(level); !strings.HasPrefix(c, "The user changed how much you say aloud.") || !strings.Contains(c, n) {
			t.Fatalf("changed %d: %q", level, c)
		}
	}
	if c := preset.AloudChanged(preset.AloudShort); !strings.Contains(c, "keep it short") {
		t.Fatalf("back to short: %q", c)
	}
}

// The settings window's values survive settings.toml: the general level and a
// character's own.
func TestAloudSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	nearly := preset.AloudNearlyAll
	err := settings.Update(path, func(s *settings.Settings) {
		s.Serve.CharacterAloud = &nearly
		s.Characters = &[]settings.Character{{Name: "Annie"}, {Name: "Greta", Aloud: preset.AloudMost}}
	})
	if err != nil {
		t.Fatal(err)
	}
	back, err := settings.Read(path)
	if err != nil || back.Serve.CharacterAloud == nil || *back.Serve.CharacterAloud != nearly || (*back.Characters)[1].Aloud != preset.AloudMost {
		t.Fatalf("settings read back: %+v, %v", back.Serve.CharacterAloud, err)
	}
	cfg, err := preset.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	annie, _ := cfg.Character("Annie")
	greta, _ := cfg.Character("Greta")
	if cfg.AloudOf(annie) != preset.AloudNearlyAll || cfg.AloudOf(greta) != preset.AloudMost {
		t.Fatalf("loaded: annie %d, greta %d", cfg.AloudOf(annie), cfg.AloudOf(greta))
	}
}
