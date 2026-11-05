// Package settings reads and writes settings.toml, the config layer owned by
// the tray and the settings window. It overlays config.toml, which stays the
// user's hand-edited file. Values use the config.toml keys; "none" disables
// a hotkey and "default" picks the system default device, since an empty
// value would not override a lower layer.
package settings

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/BurntSushi/toml"
)

type Settings struct {
	DefaultPreset *string  `toml:"default_preset,omitempty" json:"default_preset,omitempty"`
	DefaultLang   *string  `toml:"default_lang,omitempty" json:"default_lang,omitempty"`
	Volume        *float64 `toml:"volume,omitempty" json:"volume,omitempty"`
	Device        *string  `toml:"device,omitempty" json:"device,omitempty"`
	Serve         Serve    `toml:"serve,omitempty" json:"serve"`
	Listen        Listen   `toml:"listen,omitempty" json:"listen"`
	Hooks         Hooks    `toml:"hooks,omitempty" json:"hooks"`
	// Characters replaces config.toml's list when set.
	Characters *[]Character `toml:"characters,omitempty" json:"characters,omitempty"`
	// Presets are the voices tuned in the windows (named voice-...): a base
	// preset with its own speed, pitch and effects. Sent with a null value, a
	// preset is removed.
	Presets map[string]*Preset `toml:"presets,omitempty" json:"presets,omitempty"`
	// Languages the user speaks; replaces config.toml's list when set.
	Languages *[]string `toml:"languages,omitempty" json:"languages,omitempty"`
	// Sidecar holds the GPU backends' chosen options, such as qwen3's model
	// size ([sidecar.qwen3] model = "...").
	Sidecar map[string]map[string]string `toml:"sidecar,omitempty" json:"sidecar,omitempty"`
	// Servers holds the speech servers' addresses set in the Integrations
	// window ([servers] higgs = "http://...").
	Servers map[string]string `toml:"servers,omitempty" json:"servers,omitempty"`
}

// Preset is a tuned voice: what it changes on the preset it extends.
type Preset struct {
	Extends     string   `toml:"extends" json:"extends"`
	Description string   `toml:"description,omitempty" json:"description,omitempty"`
	Speed       *float64 `toml:"speed,omitempty" json:"speed,omitempty"`
	Pitch       *float64 `toml:"pitch,omitempty" json:"pitch,omitempty"`
	DSP         []string `toml:"dsp,omitempty" json:"dsp,omitempty"`
}

// TunedPrefix starts the names of the presets the windows tune.
const TunedPrefix = "voice-"

// Character is preset.Character in the settings file.
type Character struct {
	Name        string   `toml:"name" json:"name"`
	Traits      []string `toml:"traits" json:"traits"`
	Description string   `toml:"description" json:"description"`
	Preset      string   `toml:"preset" json:"preset"`
	Image       string   `toml:"image,omitempty" json:"image,omitempty"` // picture file in the avatars folder
	Color       string   `toml:"color,omitempty" json:"color,omitempty"`
}

type Serve struct {
	Duck                *float64 `toml:"duck,omitempty" json:"duck,omitempty"`
	StopHotkey          *string  `toml:"stop_hotkey,omitempty" json:"stop_hotkey,omitempty"`
	MuteHotkey          *string  `toml:"mute_hotkey,omitempty" json:"mute_hotkey,omitempty"`
	BargeIn             *bool    `toml:"barge_in,omitempty" json:"barge_in,omitempty"`
	DictateHotkey       *string  `toml:"dictate_hotkey,omitempty" json:"dictate_hotkey,omitempty"`
	DictateSubmit       *bool    `toml:"dictate_submit,omitempty" json:"dictate_submit,omitempty"`
	DictateTarget       *string  `toml:"dictate_target,omitempty" json:"dictate_target,omitempty"`
	DictateFeedback     *string  `toml:"dictate_feedback,omitempty" json:"dictate_feedback,omitempty"`
	DictateRouting      *bool    `toml:"dictate_routing,omitempty" json:"dictate_routing,omitempty"`
	DictateTheme        *string  `toml:"dictate_theme,omitempty" json:"dictate_theme,omitempty"`
	CornerSpeaking      *bool    `toml:"corner_speaking,omitempty" json:"corner_speaking,omitempty"`
	DictateOverlayScale *float64 `toml:"dictate_overlay_scale,omitempty" json:"dictate_overlay_scale,omitempty"`
	Personas            *bool    `toml:"personas,omitempty" json:"personas,omitempty"`
	SpeakingPopup       *bool    `toml:"speaking_popup,omitempty" json:"speaking_popup,omitempty"`
	PopupDictation      *bool    `toml:"popup_dictation,omitempty" json:"popup_dictation,omitempty"`
	PopupLinger         *float64 `toml:"popup_linger,omitempty" json:"popup_linger,omitempty"`
	ReaderAppear        *string  `toml:"reader_appear,omitempty" json:"reader_appear,omitempty"`
	ReaderHotkey        *string  `toml:"reader_hotkey,omitempty" json:"reader_hotkey,omitempty"`
	ReaderTheme         *string  `toml:"reader_theme,omitempty" json:"reader_theme,omitempty"`
	ReaderStoryTheme    *string  `toml:"reader_story_theme,omitempty" json:"reader_story_theme,omitempty"`
	VoiceReplies        *bool    `toml:"voice_replies,omitempty" json:"voice_replies,omitempty"`
	UnloadAfter         *string  `toml:"unload_after,omitempty" json:"unload_after,omitempty"`
	PreloadVoices       *bool    `toml:"preload_voices,omitempty" json:"preload_voices,omitempty"`
	GPUBudget           *string  `toml:"gpu_budget,omitempty" json:"gpu_budget,omitempty"`
}

type Listen struct {
	Model       *string `toml:"model,omitempty" json:"model,omitempty"`
	InputDevice *string `toml:"input_device,omitempty" json:"input_device,omitempty"`
	// Words replaces config.toml's list when set.
	Words *[]string `toml:"words,omitempty" json:"words,omitempty"`
	// Lists: the word lists turned on, by id.
	Lists *[]string `toml:"lists,omitempty" json:"lists,omitempty"`
	// Languages: where earlier versions kept the top-level languages; read
	// as long as those are unset, cleared when they are saved.
	Languages *[]string `toml:"languages,omitempty" json:"languages,omitempty"`
}

type Hooks struct {
	Enabled          *bool    `toml:"enabled,omitempty" json:"enabled,omitempty"`
	Summary          *bool    `toml:"summary,omitempty" json:"summary,omitempty"`
	SummarySentences *int     `toml:"summary_sentences,omitempty" json:"summary_sentences,omitempty"`
	SummaryFrom      *string  `toml:"summary_from,omitempty" json:"summary_from,omitempty"`
	CodexGoalWait    *float64 `toml:"codex_goal_wait,omitempty" json:"codex_goal_wait,omitempty"`
	AnnounceProject  *bool    `toml:"announce_project,omitempty" json:"announce_project,omitempty"`
	Preset           *string  `toml:"preset,omitempty" json:"preset,omitempty"`
}

var mu sync.Mutex

const header = "# Written by the voice tray and settings window. Edit config.toml for\n# anything else; values here override it.\n\n"

func Read(path string) (Settings, error) {
	var s Settings
	_, err := toml.DecodeFile(path, &s)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return s, err
}

// Update applies fn to the stored settings and writes them back atomically.
func Update(path string, fn func(*Settings)) error {
	mu.Lock()
	defer mu.Unlock()
	s, err := Read(path)
	if err != nil {
		return err
	}
	fn(&s)
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func String(v string) *string  { return &v }
func Bool(v bool) *bool        { return &v }
func Float(v float64) *float64 { return &v }
