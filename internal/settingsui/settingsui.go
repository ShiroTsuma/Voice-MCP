// Package settingsui serves the page of the settings window, the flyout the
// tray icon opens (internal/flyout). The window loads it with the process
// token in the URL once; the page then keeps it in a cookie and calls the
// JSON API below. Changes go to settings.toml and apply at once where
// possible.
package settingsui

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"voice/internal/integrations"
	"voice/internal/service"
	"voice/internal/settings"
)

//go:embed panel.html
var page []byte

//go:embed characters.html
var charactersPage []byte

//go:embed integrations.html
var integrationsPage []byte

type Deps struct {
	Token        string
	SettingsPath string
	// State returns the effective values (same keys as settings.Settings)
	// plus "options" with presets, devices, models and "notes".
	State func() map[string]any
	// Reload re-reads the config layers and applies what it can live;
	// restart reports changes that need a restart (devices, models).
	Reload  func() (restart bool, err error)
	Test    func(preset, text string) error
	Restart func()
	// Status is the live part the flyout polls: "preset", "muted", "speaking".
	Status func() map[string]any
	// Control runs a quick action: "mute", "unmute" or "stop".
	Control func(action string) error
	// Session sets what one dictation session is excluded from: dictation
	// (noInput) and speaking (noSpeech); nil leaves a switch as it is.
	Session func(pid uint32, noInput, noSpeech *bool)
	// Character gives a session another character (its name); the agent learns it
	// at its next hook or voice call.
	Character func(pid uint32, name string)
	// AvatarDir holds the characters' pictures.
	AvatarDir string
	// Clone runs a voice_clone action (record, add, list, preview, remove).
	Clone func(ctx context.Context, a service.CloneArgs) (any, error)
	// Integrations is the Integrations window's list.
	Integrations *integrations.Registry
	// Preview makes a passing preset from a tuning not saved yet and returns
	// its name (PreviewPreset), for Hear it.
	Preview func(p settings.Preset) string
}

// PreviewPreset is the passing preset Hear it plays a tuning with.
const PreviewPreset = "voice-preview"

const cookie = "voice_token"

func Register(mux *http.ServeMux, d Deps) {
	authed := func(r *http.Request) bool {
		if c, err := r.Cookie(cookie); err == nil && c.Value == d.Token {
			return true
		}
		return r.Header.Get("X-Voice-Token") == d.Token
	}
	// A page opened with the token in its URL keeps it in a cookie and drops it
	// from the address.
	servePage := func(path string, body []byte) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if t := r.URL.Query().Get("t"); t == d.Token {
				http.SetCookie(w, &http.Cookie{Name: cookie, Value: d.Token, Path: "/settings", HttpOnly: true, SameSite: http.SameSiteStrictMode})
				// Keep the token out of the page's address and history; other
				// parameters (popup=1) stay.
				q := r.URL.Query()
				q.Del("t")
				to := path
				if len(q) > 0 {
					to += "?" + q.Encode()
				}
				http.Redirect(w, r, to, http.StatusSeeOther)
				return
			}
			if !authed(r) {
				http.Error(w, "open the settings from the voice tray icon", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(body)
		})
	}
	servePage("/settings", page)
	servePage("/settings/characters", charactersPage)
	servePage("/settings/integrations", integrationsPage)
	// Character pictures, by the names saveAvatar gives them.
	mux.HandleFunc("/settings/avatar/", func(w http.ResponseWriter, r *http.Request) {
		path, err := avatarPath(d.AvatarDir, strings.TrimPrefix(r.URL.Path, "/settings/avatar/"))
		if !authed(r) || err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=31536000, immutable") // the name is the content's hash
		http.ServeFile(w, r, path)
	})
	api := func(path string, fn func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc("/settings/api/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet && r.Header.Get("Content-Type") != "application/json" {
				// Blocks form posts from other sites even with the cookie.
				http.Error(w, "json only", http.StatusUnsupportedMediaType)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fn(w, r)
		})
	}
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(v)
	}
	api("state", func(w http.ResponseWriter, r *http.Request) { reply(w, d.State(), nil) })
	api("avatar", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Data string `json:"data"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20)).Decode(&in); err != nil {
			reply(w, nil, fmt.Errorf("the picture is too large or broken"))
			return
		}
		name, err := saveAvatar(d.AvatarDir, in.Data)
		reply(w, map[string]string{"image": name}, err)
	})
	// clone records or takes a sound file and clones the voice in it; a file
	// arrives as base64 and is stored for the clone to read.
	api("clone", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			service.CloneArgs
			File string `json:"file"` // base64 of the sound file, for add
			Ext  string `json:"ext"`  // its extension: .wav, .mp3, .flac, .ogg, .m4a
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&in); err != nil {
			reply(w, nil, fmt.Errorf("the file is too large (64 MB at most) or broken"))
			return
		}
		if d.Clone == nil {
			reply(w, nil, fmt.Errorf("cloning runs in the background process"))
			return
		}
		a := in.CloneArgs
		if in.File != "" {
			raw, err := base64.StdEncoding.DecodeString(in.File)
			if err != nil {
				reply(w, nil, fmt.Errorf("the file did not arrive whole"))
				return
			}
			ext := strings.ToLower(in.Ext)
			if !map[string]bool{".wav": true, ".mp3": true, ".flac": true, ".ogg": true, ".m4a": true}[ext] {
				ext = ".wav"
			}
			f, err := os.CreateTemp("", "voice-clone-*"+ext)
			if err != nil {
				reply(w, nil, err)
				return
			}
			f.Write(raw)
			f.Close()
			defer os.Remove(f.Name())
			a.Action, a.ClipPath = "add", f.Name()
		}
		a.Rights = "confirmed by the user in the settings window"
		res, err := d.Clone(r.Context(), a)
		if err != nil && strings.Contains(err.Error(), "ffmpeg") {
			err = fmt.Errorf("voice cannot read this file as sound; use a WAV, MP3, FLAC, OGG or M4A recording")
		}
		reply(w, res, err)
	})
	api("save", func(w http.ResponseWriter, r *http.Request) {
		var in settings.Settings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			reply(w, nil, err)
			return
		}
		err := settings.Update(d.SettingsPath, func(s *settings.Settings) { merge(s, in) })
		if err != nil {
			reply(w, nil, err)
			return
		}
		restart, err := d.Reload()
		reply(w, map[string]any{"restart": restart, "state": d.State()}, err)
	})
	api("test", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Preset, Text string
			Tune         *settings.Preset // hear a tuning before it is saved
		}
		json.NewDecoder(r.Body).Decode(&in)
		if in.Tune != nil && d.Preview != nil {
			in.Preset = d.Preview(*in.Tune)
		}
		reply(w, map[string]bool{"ok": true}, d.Test(in.Preset, in.Text))
	})
	api("restart", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, nil)
		go d.Restart()
	})
	api("status", func(w http.ResponseWriter, r *http.Request) { reply(w, d.Status(), nil) })
	api("integrations", func(w http.ResponseWriter, r *http.Request) {
		if d.Integrations == nil {
			reply(w, nil, fmt.Errorf("integrations run in the background process"))
			return
		}
		// The languages the user speaks, so the window can say what speaks them.
		reply(w, map[string]any{"groups": integrations.Groups, "items": d.Integrations.List(), "languages": d.State()["languages"]}, nil)
	})
	api("integration", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Action string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || d.Integrations == nil {
			reply(w, nil, fmt.Errorf("integration needs id and action"))
			return
		}
		job, err := d.Integrations.Start(in.ID, in.Action)
		reply(w, job, err)
	})
	// Word lists: import one (a JSON file the window read) or remove an
	// imported one; turning lists on and off is a save of listen.lists.
	api("wordlists", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Action, ID, Name, Data string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			reply(w, nil, err)
			return
		}
		var err error
		switch in.Action {
		case "import":
			var wl service.WordList
			if wl, err = service.ImportWordList([]byte(in.Data), in.Name); err == nil {
				// An imported list is on right away, next to the ones on now.
				on := []string{wl.ID}
				if lists, ok := d.State()["wordlists"].([]map[string]any); ok {
					for _, l := range lists {
						if id, _ := l["id"].(string); l["on"] == true && id != wl.ID {
							on = append(on, id)
						}
					}
				}
				if err = settings.Update(d.SettingsPath, func(s *settings.Settings) { s.Listen.Lists = &on }); err == nil {
					_, err = d.Reload()
				}
			}
		case "remove":
			err = service.RemoveWordList(in.ID)
		default:
			err = fmt.Errorf("unknown action %q", in.Action)
		}
		if err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, map[string]any{"state": d.State()}, nil)
	})
	api("control", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Action string }
		json.NewDecoder(r.Body).Decode(&in)
		err := d.Control(in.Action)
		reply(w, d.Status(), err)
	})
	api("session", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PID       uint32 `json:"pid"`
			NoInput   *bool  `json:"no_input"`
			NoSpeech  *bool  `json:"no_speech"`
			Character string `json:"character"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.PID == 0 {
			reply(w, nil, fmt.Errorf("session needs pid"))
			return
		}
		if d.Session != nil {
			d.Session(in.PID, in.NoInput, in.NoSpeech)
		}
		if in.Character != "" && d.Character != nil {
			d.Character(in.PID, in.Character)
		}
		reply(w, d.State()["agents"], nil)
	})
}

// merge copies the fields the page sent over the stored ones.
func merge(s *settings.Settings, in settings.Settings) {
	set := func(dst **string, src *string) {
		if src != nil {
			*dst = src
		}
	}
	setF := func(dst **float64, src *float64) {
		if src != nil {
			*dst = src
		}
	}
	setB := func(dst **bool, src *bool) {
		if src != nil {
			*dst = src
		}
	}
	set(&s.DefaultPreset, in.DefaultPreset)
	set(&s.DefaultLang, in.DefaultLang)
	setF(&s.Volume, in.Volume)
	set(&s.Device, in.Device)
	setF(&s.Serve.Duck, in.Serve.Duck)
	set(&s.Serve.StopHotkey, in.Serve.StopHotkey)
	set(&s.Serve.MuteHotkey, in.Serve.MuteHotkey)
	setB(&s.Serve.BargeIn, in.Serve.BargeIn)
	set(&s.Serve.DictateHotkey, in.Serve.DictateHotkey)
	setB(&s.Serve.DictateSubmit, in.Serve.DictateSubmit)
	set(&s.Serve.DictateTarget, in.Serve.DictateTarget)
	set(&s.Serve.DictateFeedback, in.Serve.DictateFeedback)
	setB(&s.Serve.DictateRouting, in.Serve.DictateRouting)
	set(&s.Serve.DictateTheme, in.Serve.DictateTheme)
	if in.Characters != nil {
		s.Characters = in.Characters
	}
	// Only the windows' own tuned voices; a preset of the user's in
	// config.toml is never touched from here.
	for name, p := range in.Presets {
		if !strings.HasPrefix(name, settings.TunedPrefix) || name == PreviewPreset {
			continue
		}
		if p == nil {
			delete(s.Presets, name)
			continue
		}
		if s.Presets == nil {
			s.Presets = map[string]*settings.Preset{}
		}
		s.Presets[name] = p
	}
	setF(&s.Serve.DictateOverlayScale, in.Serve.DictateOverlayScale)
	setB(&s.Serve.Personas, in.Serve.Personas)
	setB(&s.Serve.SpeakingPopup, in.Serve.SpeakingPopup)
	setF(&s.Serve.PopupLinger, in.Serve.PopupLinger)
	setB(&s.Serve.PopupDictation, in.Serve.PopupDictation)
	setB(&s.Serve.VoiceReplies, in.Serve.VoiceReplies)
	set(&s.Serve.UnloadAfter, in.Serve.UnloadAfter)
	set(&s.Listen.Model, in.Listen.Model)
	set(&s.Listen.InputDevice, in.Listen.InputDevice)
	if in.Listen.Lists != nil {
		s.Listen.Lists = in.Listen.Lists
	}
	if in.Languages != nil {
		s.Languages = in.Languages
		s.Listen.Languages = nil
	}
	if in.Listen.Words != nil {
		s.Listen.Words = in.Listen.Words
	}
	setB(&s.Hooks.Enabled, in.Hooks.Enabled)
	setB(&s.Hooks.Summary, in.Hooks.Summary)
	if in.Hooks.SummarySentences != nil {
		s.Hooks.SummarySentences = in.Hooks.SummarySentences
	}
	set(&s.Hooks.SummaryFrom, in.Hooks.SummaryFrom)
	setF(&s.Hooks.CodexGoalWait, in.Hooks.CodexGoalWait)
	setB(&s.Hooks.AnnounceProject, in.Hooks.AnnounceProject)
	set(&s.Hooks.Preset, in.Hooks.Preset)
}
