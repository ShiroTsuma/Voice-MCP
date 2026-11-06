package settings

import "testing"

func TestRenamePreset(t *testing.T) {
	str := func(s string) *string { return &s }
	chars := []Character{{Name: "Margot", Preset: "margot-a"}, {Name: "Anna", Preset: "voice-anna"}}
	s := Settings{
		DefaultPreset: str("margot-a"),
		Hooks:         Hooks{Preset: str("calm-mature")},
		Characters:    &chars,
		Presets:       map[string]*Preset{"voice-margot": {Extends: "margot-a"}, "voice-anna": {Extends: "calm-mature"}},
	}
	if !RenamePreset(&s, "margot-a", "margot") {
		t.Fatal("nothing changed")
	}
	if *s.DefaultPreset != "margot" || chars[0].Preset != "margot" || s.Presets["voice-margot"].Extends != "margot" {
		t.Errorf("not renamed: default %q, character %q, tuning %q", *s.DefaultPreset, chars[0].Preset, s.Presets["voice-margot"].Extends)
	}
	if *s.Hooks.Preset != "calm-mature" || chars[1].Preset != "voice-anna" || s.Presets["voice-anna"].Extends != "calm-mature" {
		t.Error("other voices changed")
	}
	if RenamePreset(&s, "nobody", "x") {
		t.Error("a name nothing uses reports a change")
	}
}
