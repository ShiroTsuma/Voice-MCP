package core

import (
	"strings"

	"voice/internal/clones"
	"voice/internal/engine"
	"voice/internal/preset"
)

const clonePrefix = "clone:"

// ClonePresetName is the preset every cloned voice gets automatically.
func ClonePresetName(name string) string { return "clone-" + name }

// ClonePreset speaks with a cloned voice; when its engine is unavailable the
// neutral English voice takes over rather than going silent.
func ClonePreset(p clones.Profile) preset.Preset {
	chime := "soft"
	return preset.Preset{
		Description: "Cloned voice " + p.Name + " (from " + p.Source + ")",
		Engine:      p.Engine,
		Voice:       preset.Voice{Voice: engine.Voice{Name: clonePrefix + p.Name}},
		Chime:       &chime,
		Fallback:    []string{"kokoro", "sapi"},
	}
}

// RegisterClones adds a preset for every stored clone that the user config
// does not already define.
func RegisterClones(cfg *preset.Config) {
	for _, p := range clones.List() {
		if name := ClonePresetName(p.Name); !cfg.HasPreset(name) {
			cfg.SetPreset(name, ClonePreset(p))
		}
	}
}

// resolveVoice turns "clone:<name>" into the profile's clip and transcript.
func resolveVoice(v engine.Voice) (engine.Voice, error) {
	name, ok := strings.CutPrefix(v.Name, clonePrefix)
	if !ok {
		return v, nil
	}
	p, err := clones.Get(name)
	if err != nil {
		return engine.Voice{}, err
	}
	return engine.Voice{Ref: p.Ref, RefText: p.Text}, nil
}
