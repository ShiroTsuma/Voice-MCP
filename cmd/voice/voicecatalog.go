package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"voice/internal/engine"
	"voice/internal/engine/sherpa"
	"voice/internal/preset"
)

// The voice catalog: every voice of the engines built into voice, grouped
// by engine, for the Voices window (turn groups or voices off), the voice
// pickers (the ones on, downloaded and able to run here) and list_voices.
// A voice's id is engine/voice (kokoro/af_bella), which works wherever a
// preset's name does.

type catalogVoice struct {
	ID     string `json:"id"`
	Name   string `json:"name"`             // as people read it: "Bella"
	Lang   string `json:"lang,omitempty"`   // en-us, pl, or "" for several
	Gender string `json:"gender,omitempty"` // female, male
	On     bool   `json:"on"`               // offered in the pickers (not turned off)
	Ready  bool   `json:"ready"`            // its engine is downloaded and can speak here
}

type catalogGroup struct {
	Key     string         `json:"key"`     // the switch's id: kokoro, piper, ...
	Title   string         `json:"title"`   // Kokoro · American English
	About   string         `json:"about"`   // one line
	Engines []string       `json:"engines"` // what turning the group off turns off (engine/*)
	Ready   bool           `json:"ready"`
	Needs   string         `json:"needs,omitempty"` // what to do when it is not ready
	Voices  []catalogVoice `json:"voices"`
}

// modelReady reports whether a sherpa engine's model is downloaded.
func modelReady(cfg *preset.Config, eng string) bool {
	e, ok := cfg.Engines[eng]
	if !ok {
		return false
	}
	dir := e.String("model")
	if dir == "" {
		return false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(engine.Dirs.Models, dir)
	}
	_, err := os.Stat(dir)
	return err == nil
}

// voiceCatalog lists the built-in engines' voices. sapi lists Windows' own
// voices (nil where there are none).
func voiceCatalog(cfg *preset.Config, sapi func() []engine.VoiceInfo) []catalogGroup {
	var out []catalogGroup
	add := func(g catalogGroup) {
		for i := range g.Voices {
			g.Voices[i].On = cfg.VoiceOn(g.Voices[i].ID)
			g.Voices[i].Ready = g.Ready
		}
		if len(g.Voices) > 0 {
			out = append(out, g)
		}
	}
	title := func(n string) string { return strings.ToUpper(n[:1]) + n[1:] }

	// Kokoro: its English voices, American on the kokoro engine, British on
	// kokoro-gb (each needs its own pronunciation); the others need theirs.
	for _, k := range []struct{ eng, prefix, lang, title string }{
		{"kokoro", "a", "en-us", "Kokoro · American English"},
		{"kokoro-gb", "b", "en-gb", "Kokoro · British English"},
	} {
		if _, ok := cfg.Engines[k.eng]; !ok {
			continue
		}
		g := catalogGroup{Key: k.eng, Title: k.title, About: "Natural voices on the processor", Engines: []string{k.eng}, Ready: modelReady(cfg, k.eng)}
		if !g.Ready {
			g.Needs = "Download Kokoro in Integrations, CPU voices"
		}
		for _, v := range sherpa.KokoroVoices() {
			if strings.HasPrefix(v.Name, k.prefix) {
				g.Voices = append(g.Voices, catalogVoice{ID: k.eng + "/" + v.Name, Name: title(v.Name[3:]), Lang: k.lang, Gender: v.Gender})
			}
		}
		add(g)
	}
	if _, ok := cfg.Engines["supertonic"]; ok {
		g := catalogGroup{Key: "supertonic", Title: "Supertonic 3", About: "31 languages on the processor, the language chosen per sentence", Engines: []string{"supertonic"}, Ready: modelReady(cfg, "supertonic")}
		if !g.Ready {
			g.Needs = "Download Supertonic 3 in Integrations, CPU voices"
		}
		for i := 0; i < sherpa.SupertonicVoices; i++ {
			g.Voices = append(g.Voices, catalogVoice{ID: fmt.Sprintf("supertonic/%d", i), Name: fmt.Sprintf("Voice %d", i+1)})
		}
		add(g)
	}
	// Piper's Polish voices: an engine each, all on the Piper Polish card.
	piper := catalogGroup{Key: "piper", Title: "Piper Polish", About: "Polish voices on the processor", Ready: true}
	for _, p := range []struct{ eng, name, gender string }{
		{"piper-gosia", "Gosia", "female"}, {"piper-justyna", "Justyna", "female"}, {"piper-darkman", "Darkman", "male"},
	} {
		if _, ok := cfg.Engines[p.eng]; !ok || !modelReady(cfg, p.eng) {
			continue // each is its own download; only those downloaded are voices here
		}
		piper.Engines = append(piper.Engines, p.eng)
		piper.Voices = append(piper.Voices, catalogVoice{ID: p.eng + "/default", Name: p.name, Lang: "pl", Gender: p.gender})
	}
	add(piper)
	if sapi != nil {
		if _, ok := cfg.Engines["sapi"]; ok {
			g := catalogGroup{Key: "sapi", Title: "Windows", About: "The voices Windows has installed", Engines: []string{"sapi"}, Ready: true}
			for _, v := range sapi() {
				g.Voices = append(g.Voices, catalogVoice{ID: "sapi/" + v.Name, Name: strings.TrimPrefix(strings.TrimSuffix(v.Name, " Desktop"), "Microsoft "), Lang: strings.ToLower(v.Lang), Gender: strings.ToLower(v.Gender)})
			}
			add(g)
		}
	}
	return out
}

// voicesOn is list_voices' answer: the voices on and ready here, by engine.
func voicesOn(groups []catalogGroup) map[string]any {
	rows := [][]string{}
	for _, g := range groups {
		for _, v := range g.Voices {
			if v.On && v.Ready {
				rows = append(rows, []string{v.ID, v.Name, g.Title, v.Lang, v.Gender})
			}
		}
	}
	return map[string]any{
		"columns": []string{"id", "name", "engine", "lang", "gender"},
		"rows":    rows,
		"note":    "An id works as voice_speak's voice or a scene line's voice, and as a preset; list_presets has the tuned and cloned voices.",
	}
}

// sapiVoices lists Windows' voices through the sapi engine, briefly.
func sapiVoices(get func(string) (engine.Engine, error)) func() []engine.VoiceInfo {
	return func() []engine.VoiceInfo {
		e, err := get("sapi")
		if err != nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		v, _ := e.Voices(ctx)
		return v
	}
}
