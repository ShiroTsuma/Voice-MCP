package core

import (
	"errors"
	"fmt"
	"os"
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

// ErrVoiceGone: a preset's cloned voice or reference clip no longer exists.
var ErrVoiceGone = errors.New("voice is gone")

// resolveVoice turns "clone:<name>" into the profile's clip and transcript.
func resolveVoice(v engine.Voice) (engine.Voice, error) {
	if name, ok := strings.CutPrefix(v.Name, clonePrefix); ok {
		p, err := clones.Get(name)
		if errors.Is(err, clones.ErrNotFound) {
			return engine.Voice{}, fmt.Errorf("%w: %w", ErrVoiceGone, err)
		}
		if err != nil {
			return engine.Voice{}, err
		}
		v = engine.Voice{Ref: p.Ref, RefText: p.Text}
	}
	if v.Ref != "" {
		if _, err := os.Stat(v.Ref); errors.Is(err, os.ErrNotExist) {
			return engine.Voice{}, fmt.Errorf("%w: reference clip %s does not exist", ErrVoiceGone, v.Ref)
		}
	}
	return v, nil
}

// voiceOrDefault returns p when its voice for lang exists. When the voice was
// removed (a deleted clone, a moved clip) the default preset speaks instead,
// with a note saying so, so a character whose voice is gone is not silenced.
func voiceOrDefault(cfg *preset.Config, p preset.Resolved, lang string) (preset.Resolved, string, error) {
	_, err := resolveVoice(p.For(lang).Voice)
	if err == nil || !errors.Is(err, ErrVoiceGone) || p.Name == cfg.DefaultPreset {
		return p, "", err
	}
	d, derr := cfg.Resolve(cfg.DefaultPreset)
	if derr != nil {
		return p, "", err
	}
	if _, derr := resolveVoice(d.For(lang).Voice); derr != nil {
		return p, "", err
	}
	what := "its voice"
	if name, ok := strings.CutPrefix(p.For(lang).Voice.Name, clonePrefix); ok {
		what = "its cloned voice " + name
	}
	return d, fmt.Sprintf("preset %s spoke with the default voice: %s no longer exists; choose another voice for it in the voice settings", p.Name, what), nil
}
