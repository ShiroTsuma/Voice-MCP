// Package preset loads engines and voice presets: the built-in set embedded
// in the binary, overlaid by the user's config.toml.
package preset

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"voice/internal/audio"
	"voice/internal/engine"
	"voice/sidecar"
)

//go:embed builtin.toml
var builtin string

type Config struct {
	DefaultPreset string   `toml:"default_preset"`
	MaxChars      int      `toml:"max_chars"`
	DefaultLang   string   `toml:"default_lang"` // used when a request names no language; "auto" detects
	Volume        *float64 `toml:"volume"`
	Device        string   `toml:"device"` // output device name substring; empty = default
	// Storage. data_root moves everything at once (default: the folder of
	// voice.exe; relative to it when relative); each *_dir overrides one
	// part and is relative to data_root when relative.
	DataRoot   string                   `toml:"data_root"`
	ModelsDir  string                   `toml:"models_dir"`  // TTS/ASR models (GBs); default <data_root>\models
	RuntimeDir string                   `toml:"runtime_dir"` // sherpa-onnx DLLs; default <data_root>\runtime
	VoicesDir  string                   `toml:"voices_dir"`  // cloned voices; default <data_root>\voices
	StateDir   string                   `toml:"state_dir"`   // phrase cache, shared mute; default <data_root>\data
	SidecarDir string                   `toml:"sidecar_dir"` // Python venv and GPU weights (GBs); default <data_root>\sidecar
	Engines    map[string]engine.Config `toml:"engines"`
	// Languages the user speaks (en, pl, ...): speech recognition keeps to
	// them, and the windows say which voices and engines speak them. A later
	// layer's list replaces it.
	Languages []string `toml:"languages"`
	// LanguageNotice is the language situation the user acknowledged in the
	// settings window ("summaries" or "quality"); the window's warning stays
	// up until it matches.
	LanguageNotice string `toml:"language_notice"`
	// Sidecar holds the settings of the GPU backends (sidecar/backends.json),
	// such as qwen3's model size: [sidecar.qwen3] model = "...".
	Sidecar map[string]map[string]string `toml:"sidecar"`
	// Servers holds the address of each speech server engine (type openai)
	// set in the Integrations window: [servers] higgs = "http://...". It
	// replaces only that engine's url, keeping the rest of its table.
	Servers map[string]string `toml:"servers"`
	Presets map[string]Preset `toml:"presets"`
	Hooks   Hooks             `toml:"hooks"`
	Listen  Listen            `toml:"listen"`
	Clients map[string]string `toml:"clients"` // client name substring -> preset
	Serve   Serve             `toml:"serve"`
	// Characters are who agent sessions become, in order: a new session takes
	// the first one no other session has. A later layer's list replaces it.
	Characters []Character `toml:"characters"`
	// Traits are the personality chips the characters window offers; a later
	// layer's list replaces it. Characters may also carry traits typed in.
	Traits []string `toml:"traits"`

	// mu guards Presets, which grow at runtime when voices are cloned.
	mu sync.RWMutex
}

// Hooks configures `voice hook`, which client hooks run to announce events
// the model does not speak about itself.
type Hooks struct {
	Enabled          *bool               `toml:"enabled"`
	Preset           string              `toml:"preset"`            // empty: VOICE_PRESET or default_preset
	SkipAfterSpeech  *float64            `toml:"skip_after_speech"` // seconds; the agent just spoke
	MinTurnSeconds   *float64            `toml:"min_turn_seconds"`  // Claude Stop: only after longer turns
	Phrases          map[string][]string `toml:"phrases"`           // permission | input | done ...: replaces the phrases files' list in every language
	Summary          *bool               `toml:"summary"`           // say what the agent finished, in its words
	SummarySentences int                 `toml:"summary_sentences"` // how many sentences of it (default 2)
	SummaryFrom      string              `toml:"summary_from"`      // "end" (default): its last sentences; "start": its first
	CodexGoalWait    *float64            `toml:"codex_goal_wait"`   // seconds to wait after a Codex turn of a thread with a goal (default 3; 0 announces at once)
	AnnounceProject  *bool               `toml:"announce_project"`  // start with the project folder name
}

// Serve configures the background process that owns audio and models.
type Serve struct {
	UnloadAfter string `toml:"unload_after"` // free idle models after this long, e.g. "15m"; "0" keeps them
	// PreloadVoices loads the voices the characters and the default preset
	// use when voice starts, GPU servers included.
	PreloadVoices *bool `toml:"preload_voices"`
	// GPUBudget keeps the GPU voices loaded at once within the graphics card's
	// memory: "" or "off" no limit, "one" one at a time, or a number of GB
	// (an empty value cannot override a lower layer, hence "off"). A voice
	// that would not fit frees the least recently used ones first.
	GPUBudget  string   `toml:"gpu_budget"`
	Duck       *float64 `toml:"duck"`        // other apps' volume while speaking, 0..1; 1 turns ducking off
	StopHotkey string   `toml:"stop_hotkey"` // e.g. "Ctrl+Alt+End"; empty disables
	MuteHotkey string   `toml:"mute_hotkey"` // toggles mute; empty disables
	BargeIn    *bool    `toml:"barge_in"`    // stop speaking when the user starts talking (use headphones)
	// Dictation: the hotkey records what the user says and types it into an agent.
	DictateHotkey string `toml:"dictate_hotkey"` // starts recording; pressed again, ends it; empty disables
	DictateSubmit *bool  `toml:"dictate_submit"` // press Enter after the text, sending it to the agent
	DictateTarget string `toml:"dictate_target"` // auto, console or window (see config.example.toml)
	// DictateFeedback: screen (an overlay in the screen corner), voice or both.
	DictateFeedback string `toml:"dictate_feedback"`
	// DictateRouting: a message that starts with a session's name, client or
	// project ("Annie, ...", "Codex, ...") goes to that agent only.
	DictateRouting *bool `toml:"dictate_routing"`
	// DictateTheme: how the overlay looks (overlay.Themes names).
	DictateTheme string `toml:"dictate_theme"`
	// CornerSpeaking: while an agent speaks and no popup or settings window
	// shows it, a smaller ring in the corner animation moves with its voice
	// (default on).
	CornerSpeaking *bool `toml:"corner_speaking"`
	// DictateOverlayScale: the overlay's size on screen, 1 = the theme's canvas.
	DictateOverlayScale *float64 `toml:"dictate_overlay_scale"`
	// Personas: sessions become their character (tone and voice); off keeps
	// the names for dictation only.
	Personas *bool `toml:"personas"`
	// SpeakingPopup shows who speaks and what, in a small window in the corner,
	// while an agent speaks (off by default).
	SpeakingPopup *bool `toml:"speaking_popup"`
	// PopupDictation also shows the popup while the user dictates, with
	// their words as they are heard (off by default).
	PopupDictation *bool `toml:"popup_dictation"`
	// PopupLinger is how many seconds the popup stays after the voice stops
	// (default 8).
	PopupLinger *float64 `toml:"popup_linger"`

	// ReaderAppear is how the words show in it: "words" (each as it is said)
	// or "block" (all at once, the word being said marked).
	ReaderAppear *string `toml:"reader_appear"`
	// ReaderHotkey shows the reading window and hides it again, like a game
	// overlay; shown so, it scrolls and stays until the key hides it.
	ReaderHotkey string `toml:"reader_hotkey"`
	// ReaderTheme is a folder under themes in the data folder whose theme.css
	// restyles the reading window; empty keeps the built-in look.
	ReaderTheme *string `toml:"reader_theme"`
	// ReaderStoryTheme is the theme when the window shows one session that
	// tells a story (narrative style); empty uses ReaderTheme.
	ReaderStoryTheme *string `toml:"reader_story_theme"`
	// VoiceReplies has an agent say its answer aloud when the user dictated the
	// message, and read its questions aloud (off by default).
	VoiceReplies *bool `toml:"voice_replies"`
}

// Listen configures speech recognition for voice_listen and transcripts.
type Listen struct {
	Model       string   `toml:"model"`        // ASR model directory under models_dir
	Language    string   `toml:"language"`     // Whisper only; empty detects
	InputDevice string   `toml:"input_device"` // microphone name substring; empty = default
	EndSilence  *float64 `toml:"end_silence"`  // seconds of silence that end an answer
	// Words the recognizer should get right: names, terms, or "heard -> meant".
	Words []string `toml:"words"`
	// Lists are the word lists turned on (their ids); unset, the default
	// ones in a language the user speaks.
	Lists   []string `toml:"lists"`
	Threads int      `toml:"threads"`
	// Languages recognition keeps to: the top-level languages (set at load).
	// [listen] languages from earlier versions is read when that is unset.
	Languages []string `toml:"languages"`
}

// Preset is one voice character. Unset fields inherit from Extends.
type Preset struct {
	Extends     string                  `toml:"extends"`
	Description string                  `toml:"description"`
	Engine      string                  `toml:"engine"`
	Voice       Voice                   `toml:"voice"`
	Speed       *float64                `toml:"speed"`
	Volume      *float64                `toml:"volume"`
	Pitch       *float64                `toml:"pitch"` // semitones, + is higher
	DSP         []string                `toml:"dsp"`   // effect chain, see audio.ParseChain
	Chime       *string                 `toml:"chime"`
	Fallback    []string                `toml:"fallback"`
	Lang        map[string]LangOverride `toml:"lang"`
}

type LangOverride struct {
	Engine string   `toml:"engine"`
	Voice  Voice    `toml:"voice"`
	Speed  *float64 `toml:"speed"`
	Pitch  *float64 `toml:"pitch"`
}

// Voice accepts `voice = "name"`, `voice = "ref:clip.wav"` or a mix table
// `voice = { af_nicole = 0.6, bf_emma = 0.4 }`.
type Voice struct{ engine.Voice }

func (v *Voice) UnmarshalTOML(data any) error {
	switch x := data.(type) {
	case string:
		if ref, ok := strings.CutPrefix(x, "ref:"); ok {
			v.Ref = ref
		} else {
			v.Name = x
		}
	case map[string]any:
		v.Mix = map[string]float64{}
		for name, w := range x {
			switch n := w.(type) {
			case float64:
				v.Mix[name] = n
			case int64:
				v.Mix[name] = float64(n)
			default:
				return fmt.Errorf("voice mix %q: weight must be a number", name)
			}
		}
	default:
		return fmt.Errorf("voice: expected string or table, got %T", data)
	}
	return nil
}

// Load reads the built-in configuration and overlays each existing file in
// order: the user's config.toml, then settings.toml written by the settings
// page and tray.
func Load(paths ...string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.Decode(builtin, cfg); err != nil {
		return nil, fmt.Errorf("builtin config: %w", err)
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		user := &Config{}
		md, err := toml.Decode(string(data), user)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if keys := md.Undecoded(); len(keys) > 0 {
			var unknown []string
			for _, k := range keys {
				// Engine tables are free-form; only report keys outside them.
				if len(k) > 0 && k[0] != "engines" {
					unknown = append(unknown, k.String())
				}
			}
			if len(unknown) > 0 {
				return nil, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(unknown, ", "))
			}
		}
		cfg.merge(user)
	}
	// Languages are general; recognition reads them through Listen.
	if cfg.Languages == nil {
		cfg.Languages = cfg.Listen.Languages
	}
	// Never chosen: the system's language and English. An empty list a
	// layer saved ("none: any") stays empty.
	if cfg.Languages == nil {
		cfg.Languages = DefaultLanguages(systemLanguage())
	}
	cfg.Listen.Languages = cfg.Languages
	// The GPU backends' engines come from sidecar/backends.json, with the
	// user's settings; an [engines.<name>] table in config.toml replaces one.
	if cfg.Engines == nil {
		cfg.Engines = map[string]engine.Config{}
	}
	for _, b := range sidecar.Backends() {
		if _, ok := cfg.Engines[b.Name]; !ok {
			cfg.Engines[b.Name] = b.Engine(cfg.Sidecar[b.Name])
		}
	}
	for name, url := range cfg.Servers {
		if e, ok := cfg.Engines[name]; ok && strings.TrimSpace(url) != "" {
			c := engine.Config{}
			for k, v := range e {
				c[k] = v
			}
			c["url"] = strings.TrimSpace(url)
			cfg.Engines[name] = c
		}
	}
	return cfg, nil
}
func (c *Config) merge(o *Config) {
	if o.DefaultPreset != "" {
		c.DefaultPreset = o.DefaultPreset
	}
	if o.MaxChars != 0 {
		c.MaxChars = o.MaxChars
	}
	if o.DefaultLang != "" {
		c.DefaultLang = o.DefaultLang
	}
	if o.Volume != nil {
		c.Volume = o.Volume
	}
	if o.Device != "" {
		c.Device = o.Device
	}
	for _, p := range []struct{ dst, src *string }{
		{&c.DataRoot, &o.DataRoot}, {&c.ModelsDir, &o.ModelsDir}, {&c.RuntimeDir, &o.RuntimeDir},
		{&c.VoicesDir, &o.VoicesDir}, {&c.StateDir, &o.StateDir}, {&c.SidecarDir, &o.SidecarDir},
	} {
		if *p.src != "" {
			*p.dst = *p.src
		}
	}
	if o.Characters != nil { // "characters = []" removes them all
		c.Characters = o.Characters
	}
	if len(o.Traits) > 0 {
		c.Traits = o.Traits
	}
	for k, v := range o.Clients {
		if c.Clients == nil {
			c.Clients = map[string]string{}
		}
		c.Clients[k] = v
	}
	sv := o.Serve
	if sv.PreloadVoices != nil {
		c.Serve.PreloadVoices = sv.PreloadVoices
	}
	if sv.UnloadAfter != "" {
		c.Serve.UnloadAfter = sv.UnloadAfter
	}
	if sv.GPUBudget != "" {
		c.Serve.GPUBudget = sv.GPUBudget
	}
	if sv.Duck != nil {
		c.Serve.Duck = sv.Duck
	}
	if sv.StopHotkey != "" {
		c.Serve.StopHotkey = sv.StopHotkey
	}
	if sv.MuteHotkey != "" {
		c.Serve.MuteHotkey = sv.MuteHotkey
	}
	if sv.ReaderHotkey != "" {
		c.Serve.ReaderHotkey = sv.ReaderHotkey
	}
	if sv.BargeIn != nil {
		c.Serve.BargeIn = sv.BargeIn
	}
	if sv.DictateHotkey != "" {
		c.Serve.DictateHotkey = sv.DictateHotkey
	}
	if sv.DictateSubmit != nil {
		c.Serve.DictateSubmit = sv.DictateSubmit
	}
	if sv.DictateTarget != "" {
		c.Serve.DictateTarget = sv.DictateTarget
	}
	if sv.DictateFeedback != "" {
		c.Serve.DictateFeedback = sv.DictateFeedback
	}
	if sv.DictateRouting != nil {
		c.Serve.DictateRouting = sv.DictateRouting
	}
	if sv.CornerSpeaking != nil {
		c.Serve.CornerSpeaking = sv.CornerSpeaking
	}
	if sv.DictateTheme != "" {
		c.Serve.DictateTheme = sv.DictateTheme
	}
	if sv.DictateOverlayScale != nil {
		c.Serve.DictateOverlayScale = sv.DictateOverlayScale
	}
	if sv.Personas != nil {
		c.Serve.Personas = sv.Personas
	}
	if sv.SpeakingPopup != nil {
		c.Serve.SpeakingPopup = sv.SpeakingPopup
	}
	if sv.PopupDictation != nil {
		c.Serve.PopupDictation = sv.PopupDictation
	}
	if sv.PopupLinger != nil {
		c.Serve.PopupLinger = sv.PopupLinger
	}

	if sv.ReaderAppear != nil {
		c.Serve.ReaderAppear = sv.ReaderAppear
	}
	if sv.ReaderTheme != nil {
		c.Serve.ReaderTheme = sv.ReaderTheme
	}
	if sv.ReaderStoryTheme != nil {
		c.Serve.ReaderStoryTheme = sv.ReaderStoryTheme
	}
	if sv.VoiceReplies != nil {
		c.Serve.VoiceReplies = sv.VoiceReplies
	}
	l := o.Listen
	if l.Model != "" {
		c.Listen.Model = l.Model
	}
	if l.Language != "" {
		c.Listen.Language = l.Language
	}
	if l.InputDevice != "" {
		c.Listen.InputDevice = l.InputDevice
	}
	if l.EndSilence != nil {
		c.Listen.EndSilence = l.EndSilence
	}
	if l.Threads != 0 {
		c.Listen.Threads = l.Threads
	}
	if l.Languages != nil {
		c.Listen.Languages = l.Languages
	}
	if l.Lists != nil {
		c.Listen.Lists = l.Lists
	}
	if l.Words != nil {
		c.Listen.Words = l.Words
	}
	h := o.Hooks
	if h.Enabled != nil {
		c.Hooks.Enabled = h.Enabled
	}
	if h.Preset != "" {
		c.Hooks.Preset = h.Preset
	}
	if h.SkipAfterSpeech != nil {
		c.Hooks.SkipAfterSpeech = h.SkipAfterSpeech
	}
	if h.MinTurnSeconds != nil {
		c.Hooks.MinTurnSeconds = h.MinTurnSeconds
	}
	for k, v := range h.Phrases {
		if c.Hooks.Phrases == nil {
			c.Hooks.Phrases = map[string][]string{}
		}
		c.Hooks.Phrases[k] = v
	}
	if h.Summary != nil {
		c.Hooks.Summary = h.Summary
	}
	if h.AnnounceProject != nil {
		c.Hooks.AnnounceProject = h.AnnounceProject
	}
	if h.SummarySentences != 0 {
		c.Hooks.SummarySentences = h.SummarySentences
	}
	if h.SummaryFrom != "" {
		c.Hooks.SummaryFrom = h.SummaryFrom
	}
	if h.CodexGoalWait != nil {
		c.Hooks.CodexGoalWait = h.CodexGoalWait
	}
	for k, v := range o.Engines {
		c.Engines[k] = v
	}
	if o.Languages != nil {
		c.Languages = o.Languages
	}
	if o.LanguageNotice != "" {
		c.LanguageNotice = o.LanguageNotice
	}
	for name, set := range o.Sidecar {
		if c.Sidecar == nil {
			c.Sidecar = map[string]map[string]string{}
		}
		if c.Sidecar[name] == nil {
			c.Sidecar[name] = map[string]string{}
		}
		for k, v := range set {
			c.Sidecar[name][k] = v
		}
	}
	for k, v := range o.Servers {
		if c.Servers == nil {
			c.Servers = map[string]string{}
		}
		c.Servers[k] = v
	}
	for k, v := range o.Presets {
		c.Presets[k] = v
	}
}

// Resolved is a preset with inheritance applied and defaults filled in.
type Resolved struct {
	Name        string
	Description string
	Engine      string
	Voice       engine.Voice
	Speed       float64
	Volume      float64
	Pitch       float64
	DSP         []string
	Chime       string
	Fallback    []string
	Lang        map[string]LangOverride
}

func (c *Config) Resolve(name string) (Resolved, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resolveLocked(name)
}

// SetPreset adds or replaces a preset at runtime.
func (c *Config) SetPreset(name string, p Preset) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Presets[name] = p
}

// LegacyClonePrefix began a cloned voice's preset name in earlier versions
// (clone-<name>); the preset is now the voice's own name.
const LegacyClonePrefix = "clone-"

// PresetDef returns a preset's own definition, before extends.
func (c *Config) PresetDef(name string) (Preset, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.Presets[name]
	return p, ok
}

// HasPreset reports whether a preset exists.
func (c *Config) HasPreset(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.Presets[name]
	return ok
}

// DeletePreset removes a runtime preset.
func (c *Config) DeletePreset(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Presets, name)
}

func (c *Config) resolveLocked(name string) (Resolved, error) {
	if name == "" {
		name = c.DefaultPreset
	}
	var chain []Preset
	seen := map[string]bool{}
	for cur := name; cur != ""; {
		if seen[cur] {
			return Resolved{}, fmt.Errorf("preset %q: extends cycle through %q", name, cur)
		}
		seen[cur] = true
		p, ok := c.Presets[cur]
		if !ok {
			// A cloned voice's preset was once called clone-<name>; settings
			// and agents may still use that.
			if bare, legacy := strings.CutPrefix(cur, LegacyClonePrefix); legacy {
				if p, ok = c.Presets[bare]; ok {
					cur = bare
				}
			}
		}
		if !ok {
			if cur == name {
				return Resolved{}, fmt.Errorf("unknown preset %q (available: %s)", name, strings.Join(c.presetNamesLocked(), ", "))
			}
			return Resolved{}, fmt.Errorf("preset %q extends unknown preset %q", name, cur)
		}
		chain = append(chain, p)
		cur = p.Extends
	}
	r := Resolved{Name: name, Speed: 1, Volume: 1, Chime: "none", Lang: map[string]LangOverride{}}
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		if p.Description != "" {
			r.Description = p.Description
		}
		if p.Engine != "" {
			r.Engine = p.Engine
		}
		if !p.Voice.IsZero() {
			r.Voice = p.Voice.Voice
		}
		if p.Speed != nil {
			r.Speed = *p.Speed
		}
		if p.Volume != nil {
			r.Volume = *p.Volume
		}
		if p.Pitch != nil {
			r.Pitch = *p.Pitch
		}
		if p.DSP != nil {
			r.DSP = p.DSP
		}
		if p.Chime != nil {
			r.Chime = *p.Chime
		}
		if p.Fallback != nil {
			r.Fallback = p.Fallback
		}
		for lang, o := range p.Lang {
			r.Lang[lang] = o
		}
	}
	if c.Volume != nil {
		r.Volume *= *c.Volume
	}
	if r.Engine == "" {
		return Resolved{}, fmt.Errorf("preset %q: no engine", name)
	}
	for _, e := range append([]string{r.Engine}, r.Fallback...) {
		if _, ok := c.Engines[e]; !ok {
			return Resolved{}, fmt.Errorf("preset %q: unknown engine %q", name, e)
		}
	}
	for lang, o := range r.Lang {
		if _, ok := c.Engines[o.Engine]; o.Engine != "" && !ok {
			return Resolved{}, fmt.Errorf("preset %q lang %s: unknown engine %q", name, lang, o.Engine)
		}
	}
	if _, err := audio.ParseChain(r.DSP, audio.DeviceRate); err != nil {
		return Resolved{}, fmt.Errorf("preset %q: %w", name, err)
	}
	return r, nil
}

// Choice is what one utterance uses after language overrides.
type Choice struct {
	Engines []string // primary first, then fallbacks
	Voice   engine.Voice
	Speed   float64
	Pitch   float64
}

// For applies the language override. A voice chosen for the base engine is
// not carried to a different engine, where the name would mean nothing, and
// a language routed to its own engine does not fall back to the base engine,
// which may not speak that language; only the preset fallbacks remain.
func (r Resolved) For(lang string) Choice {
	ch := Choice{Engines: []string{r.Engine}, Voice: r.Voice, Speed: r.Speed, Pitch: r.Pitch}
	if o, ok := r.Lang[lang]; ok {
		if o.Engine != "" && o.Engine != r.Engine {
			ch.Engines[0] = o.Engine
			ch.Voice = engine.Voice{}
		}
		if !o.Voice.IsZero() {
			ch.Voice = o.Voice.Voice
		}
		if o.Speed != nil {
			ch.Speed = *o.Speed
		}
		if o.Pitch != nil {
			ch.Pitch = *o.Pitch
		}
	}
	for _, f := range r.Fallback {
		dup := false
		for _, have := range ch.Engines {
			dup = dup || have == f
		}
		if !dup {
			ch.Engines = append(ch.Engines, f)
		}
	}
	return ch
}

// MixesFor lists every voice mix any preset requests from an engine, so the
// engine can prepare them when it starts.
func (c *Config) MixesFor(engineName string) []engine.Voice {
	var out []engine.Voice
	for _, name := range c.PresetNames() {
		r, err := c.Resolve(name)
		if err != nil {
			continue
		}
		for _, lang := range append([]string{""}, mapKeys(r.Lang)...) {
			ch := r.For(lang)
			if ch.Engines[0] == engineName && len(ch.Voice.Mix) > 1 {
				out = append(out, ch.Voice)
			}
		}
	}
	return out
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *Config) PresetNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.presetNamesLocked()
}

func (c *Config) presetNamesLocked() []string {
	out := make([]string, 0, len(c.Presets))
	for n := range c.Presets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SummaryLength is how many sentences a spoken summary takes, 1 to 3.
func (h Hooks) SummaryLength() int {
	return max(1, min(3, cmpOr(h.SummarySentences, 2)))
}

// SummaryPart is which end of the agent's message a summary reads: "end"
// (its conclusion, the default) or "start".
func (h Hooks) SummaryPart() string {
	if h.SummaryFrom == "start" {
		return "start"
	}
	return "end"
}

func cmpOr(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// Linger is how long the speaking popup stays after the voice stops.
func (s Serve) Linger() time.Duration {
	v := 8.0
	if s.PopupLinger != nil {
		v = max(1, min(*s.PopupLinger, 60))
	}
	return time.Duration(v * float64(time.Second))
}

// Appear is how words show in the reading window: "words" or "block".
func (s Serve) Appear() string {
	if s.ReaderAppear != nil && *s.ReaderAppear == "block" {
		return "block"
	}
	return "words"
}

// GoalWait is how long to wait after a Codex turn of a thread with a goal
// before announcing it: the goal may start the next turn at once.
func (h Hooks) GoalWait() time.Duration {
	s := 3.0
	if h.CodexGoalWait != nil {
		s = max(0, min(*h.CodexGoalWait, 30))
	}
	return time.Duration(s * float64(time.Second))
}

// PresetExtends is the preset name extends, "" when it extends none.
func (c *Config) PresetExtends(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Presets[name].Extends
}
