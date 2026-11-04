package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"voice/internal/core"
	"voice/internal/flyout"
	"voice/internal/service"
)

// The settings window is a flyout above the tray icon.
var (
	panelOnce sync.Once
	panel     *flyout.Window
)

func (b *backend) panel() *flyout.Window {
	panelOnce.Do(func() {
		panel = flyout.New(flyout.Options{
			Width: 380, Height: 780, Margin: 12,
			URL:        func() string { return "http://" + b.info.Addr + "/settings?t=" + b.info.Token },
			DataDir:    filepath.Join(core.DataDir(), "webview"),
			Background: 0x16161d,
			OnShow:     "onPanelShow()",
			// Closed while an agent speaks: the popup takes over.
			OnHide: b.panelHidden,
			// The panel's "Manage characters" button opens the characters window.
			OnMessage: func(m string) {
				switch m {
				case "characters":
					b.showCharacters()
				case "integrations":
					b.showSettings("integrations")
				case "settings":
					b.showSettings("")
				case "settings:characters":
					b.showSettings("characters")
				}
			},
		})
	})
	return panel
}

func (b *backend) showPanel() {
	if err := b.panel().Show(); err != nil {
		logf("settings window: %v", err)
	}
	b.panelShown()
}

func (b *backend) togglePanel() {
	if err := b.panel().Toggle(); err != nil {
		logf("settings window: %v", err)
	}
	b.panelShown()
}

// panelShown puts the popup away while the settings window, which shows the
// same, is open.
func (b *backend) panelShown() {
	if b.panel().Visible() {
		if popup != nil {
			popup.Hide()
		}
		if reader != nil {
			readerPinned.Store(false)
			reader.SetClickThrough(true)
			reader.Hide()
		}
	}
}

func (b *backend) panelStatus() map[string]any {
	st := b.sp.Status()
	// The line being said, with who says it (from the transcript), so the
	// window fills it in as it is heard; the transcript's newest lines too.
	transcript := b.ctl.agents.Transcript()
	out := map[string]any{"preset": st.Preset, "muted": st.Muted, "speaking": st.Speaking, "transcript": transcript, "agents": b.ctl.liveAgents(), "reader_pinned": readerPinned.Load()}
	if d := b.ctl.Dictation(); d != nil {
		out["dictation"] = d
	}
	if l := loadingStatus(); l != nil {
		out["loading"] = l
	}
	if l := st.Line; l != nil {
		line := map[string]any{"id": l.ID, "text": l.Text, "done": l.Done, "from": l.From, "to": l.To, "at": l.At, "seconds": l.Seconds, "preparing": l.Preparing}
		for i := len(transcript) - 1; i >= 0; i-- {
			if transcript[i].ID == l.ID {
				line["pid"], line["name"], line["client"], line["project"], line["source"] = transcript[i].PID, transcript[i].Name, transcript[i].Client, transcript[i].Project, transcript[i].Source
				break
			}
		}
		out["line"] = line
	}
	return out
}

func (b *backend) control(action string) error {
	switch action {
	case "mute":
		b.sp.SetMuted(true)
	case "unmute":
		b.sp.SetMuted(false)
	case "stop":
		b.sp.Stop()
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return nil
}

// The characters window is an ordinary app window (flyout's normal mode).
var (
	charactersOnce sync.Once
	charactersWin  *flyout.Window
)

func (b *backend) showCharacters() {
	charactersOnce.Do(func() {
		charactersWin = flyout.New(flyout.Options{
			Normal: true, Title: "voice · Characters", Width: 900, Height: 640,
			URL:        func() string { return "http://" + b.info.Addr + "/settings/characters?t=" + b.info.Token },
			DataDir:    filepath.Join(core.DataDir(), "webview"),
			Background: 0x16161d,
			OnShow:     "onShow()",
		})
	})
	b.panel().Hide()
	if err := charactersWin.Show(); err != nil {
		logf("characters window: %v", err)
	}
}

// The speaking popup: the top of the settings window on its own, in the
// corner, while an agent speaks. It shows and takes nothing: no focus, no
// clicks. It leaves a while after the voice stops, and stays while the user
// dictates (their answer shows in it too).
var (
	popupOnce sync.Once
	popup     *flyout.Window
	popupMu   sync.Mutex
	popupHide *time.Timer
	// What keeps the popup up: speech, and dictation started while it shows.
	popupSpeaking, popupDictating bool
)

func (b *backend) popup() *flyout.Window {
	popupOnce.Do(func() {
		popup = flyout.New(flyout.Options{
			Width: 380, Height: 298, Margin: 12, Passive: true,
			URL:        func() string { return "http://" + b.info.Addr + "/settings?popup=1&t=" + b.info.Token },
			DataDir:    filepath.Join(core.DataDir(), "webview"),
			Background: 0x16161d,
			OnShow:     "onPanelShow()",
		})
	})
	return popup
}

// speaking shows the popup when speech starts (with the setting on and the
// settings window closed) and lets it go a while after speech ends.
func (b *backend) speaking(on bool) {
	popupMu.Lock()
	defer popupMu.Unlock()
	popupSpeaking = on
	if on {
		b.showPopupLocked()
	}
	b.lingerLocked()
	// Nothing on screen says who speaks (no popup, no settings window): the
	// agent's ring in the corner does, in its character's colour.
	sv := b.cfg.Serve
	popupOn := (sv.SpeakingPopup != nil && *sv.SpeakingPopup) || b.wantsReader()
	corner := on && (sv.CornerSpeaking == nil || *sv.CornerSpeaking) && !popupOn && !b.panel().Visible()
	b.ctl.cornerSpeech(corner, b.speakerTint())
}

// preparing shows the agent's ring working in the corner while its voice
// renders: the popup waits for the sound, so nothing else would say it.
// A fast voice is ready in a moment: the ring shows only once rendering
// takes longer than this, so it does not flash before every sentence.
func (b *backend) preparing(on bool) {
	prepMu.Lock()
	defer prepMu.Unlock()
	if prepTimer != nil {
		prepTimer.Stop()
		prepTimer = nil
	}
	if !on {
		b.ctl.cornerPreparing(false, 0)
		return
	}
	sv := b.cfg.Serve
	if (sv.CornerSpeaking != nil && !*sv.CornerSpeaking) || b.panel().Visible() {
		return
	}
	prepTimer = time.AfterFunc(800*time.Millisecond, func() {
		b.ctl.cornerPreparing(true, b.speakerTint())
	})
}

var (
	prepMu    sync.Mutex
	prepTimer *time.Timer
)

// speakerTint is the colour of the character speaking now, 0xRRGGBB, or 0.
func (b *backend) speakerTint() uint32 {
	l := b.sp.Status().Line
	if l == nil {
		return 0
	}
	name := ""
	for _, t := range b.ctl.agents.Transcript() {
		if t.ID == l.ID {
			name = t.Name
		}
	}
	for _, ch := range b.cfg.Characters {
		if name != "" && strings.EqualFold(ch.Name, name) {
			if v, err := strconv.ParseUint(strings.TrimPrefix(ch.Color, "#"), 16, 32); err == nil && len(strings.TrimPrefix(ch.Color, "#")) == 6 {
				return uint32(v)
			}
		}
	}
	return 0
}

// panelHidden brings the popup up when the settings window closes while an
// agent is still speaking.
func (b *backend) panelHidden() {
	popupMu.Lock()
	defer popupMu.Unlock()
	if popupSpeaking {
		b.showPopupLocked()
		b.lingerLocked()
	}
}

// showPopupLocked shows the popup if the setting is on and the settings
// window is closed; the caller holds popupMu.
func (b *backend) showPopupLocked() {
	if b.panel().Visible() {
		return
	}
	// A long message or a scene goes to the reading window; once it is up, it
	// stays for what follows.
	if (reader != nil && reader.Visible()) || b.wantsReader() {
		if err := b.reader().ShowPassive(); err != nil {
			logf("reading window: %v", err)
		}
		if popup != nil {
			popup.Hide()
		}
		return
	}
	if sv := b.cfg.Serve; sv.SpeakingPopup == nil || !*sv.SpeakingPopup {
		return
	}
	if err := b.popup().ShowPassive(); err != nil {
		logf("speaking popup: %v", err)
	}
}

// dictating keeps an open popup while the user dictates, and with
// [serve] popup_dictation opens it, so the user sees their words as heard.
func (b *backend) dictating(on bool) {
	popupMu.Lock()
	defer popupMu.Unlock()
	// The reading window shows the dictation too, as it is heard: when it is up
	// or on its way for the speech now, or set to show always, it does instead
	// of the corner popup.
	sv := b.cfg.Serve
	toReader := (reader != nil && reader.Visible()) || (popupSpeaking && b.wantsReader()) || sv.Reader() == "always"
	if on && sv.PopupDictation != nil && *sv.PopupDictation && !b.panel().Visible() {
		w, what := b.popup(), "speaking popup"
		if toReader {
			w, what = b.reader(), "reading window"
			if popup != nil {
				popup.Hide()
			}
		}
		if err := w.ShowPassive(); err != nil {
			logf("%s: %v", what, err)
		}
	}
	// Dictating holds the popup whenever it shows, also when it came up after
	// the dictation started (an agent began to speak).
	popupDictating = on
	b.lingerLocked()
}

// lingerLocked hides the popup a while ([serve] popup_linger) after nothing keeps it up any
// more; the caller holds popupMu.
func (b *backend) lingerLocked() {
	if popupHide != nil {
		popupHide.Stop()
		popupHide = nil
	}
	if (popup == nil && reader == nil) || popupSpeaking || popupDictating {
		return
	}
	popupHide = time.AfterFunc(b.cfg.Serve.Linger(), func() {
		if popup != nil {
			popup.Hide()
		}
		// Opened with its hotkey, the reading window stays until the key hides it.
		if reader != nil && !readerPinned.Load() {
			reader.Hide()
		}
	})
}

// The reading window: a large one in the middle of the screen with the
// recent messages, for long messages and scenes the corner is too small for.
// Like the popup it takes no focus and no clicks.
var (
	readerOnce sync.Once
	reader     *flyout.Window
	// readerPinned: the hotkey opened the window; it takes the mouse wheel and
	// stays until the hotkey hides it.
	readerPinned atomic.Bool
)

// toggleReader is the reading window's hotkey: it opens the window to read
// and scroll back, or hides it.
func (b *backend) toggleReader() {
	popupMu.Lock()
	defer popupMu.Unlock()
	w := b.reader()
	if w.Visible() && readerPinned.Load() {
		readerPinned.Store(false)
		w.SetClickThrough(true)
		w.Hide()
		return
	}
	readerPinned.Store(true)
	if err := w.ShowPassive(); err != nil {
		logf("reading window: %v", err)
		return
	}
	w.SetClickThrough(false)
	if popup != nil {
		popup.Hide()
	}
}

func (b *backend) reader() *flyout.Window {
	readerOnce.Do(func() {
		reader = flyout.New(flyout.Options{
			Width: 820, Height: 760, Margin: 24, Passive: true, Centre: true,
			URL:        func() string { return "http://" + b.info.Addr + "/settings/reader?t=" + b.info.Token },
			DataDir:    filepath.Join(core.DataDir(), "webview"),
			Background: 0x16161d,
			OnShow:     "onShow()",
		})
	})
	return reader
}

// readerLong is how many characters make a message long enough for the
// reading window ([serve] reader_window = "long").
const readerLong = 280

// wantsReader reports whether what is being said now goes to the reading
// window.
func (b *backend) wantsReader() bool {
	switch b.cfg.Serve.Reader() {
	case "always":
		return true
	case "long":
		l := b.saying()
		return l != nil && (l.Scene || utf8.RuneCountInString(l.Text) >= readerLong)
	}
	return false
}

// saying is the transcript's entry for what is being said now, or the newest
// said one while the voice gets ready.
func (b *backend) saying() *service.Line {
	t := b.ctl.agents.Transcript()
	id := ""
	if l := b.sp.Status().Line; l != nil {
		id = l.ID
	}
	for i := len(t) - 1; i >= 0; i-- {
		if t[i].Kind == "said" && (id == "" || t[i].ID == id) {
			return &t[i]
		}
	}
	return nil
}

// The Integrations window: an ordinary window like the characters one.
var (
	integrationsOnce sync.Once
	integrationsWin  *flyout.Window
)

func (b *backend) showIntegrations() {
	integrationsOnce.Do(func() {
		integrationsWin = flyout.New(flyout.Options{
			Normal: true, Title: "voice · Integrations", Width: 960, Height: 680,
			URL:        func() string { return "http://" + b.info.Addr + "/settings/integrations?t=" + b.info.Token },
			DataDir:    filepath.Join(core.DataDir(), "webview"),
			Background: 0x16161d,
			OnShow:     "onShow()",
		})
	})
	b.panel().Hide()
	if err := integrationsWin.Show(); err != nil {
		logf("integrations window: %v", err)
	}
}

// The full settings window: every setting, characters and integrations,
// beside a list of sections. The flyout keeps what is used every day.
var (
	settingsOnce    sync.Once
	settingsWin     *flyout.Window
	settingsMu      sync.Mutex
	settingsSection string // the section the next show opens, "" for the last one
)

func (b *backend) showSettings(section string) {
	settingsMu.Lock()
	settingsSection = section
	settingsMu.Unlock()
	settingsOnce.Do(func() {
		settingsWin = flyout.New(flyout.Options{
			Normal: true, Title: "voice · Settings", Width: 980, Height: 700,
			URL: func() string {
				settingsMu.Lock()
				defer settingsMu.Unlock()
				u := "http://" + b.info.Addr + "/settings?full=1&t=" + b.info.Token
				if settingsSection != "" {
					u += "&section=" + settingsSection
				}
				return u
			},
			DataDir:    filepath.Join(core.DataDir(), "webview"),
			Background: 0x16161d,
			OnShow:     "onPanelShow()",
		})
	})
	b.panel().Hide()
	if err := settingsWin.Show(); err != nil {
		logf("settings window: %v", err)
	}
}

// What engine voice waits for while its server starts and loads the model
// (httpengine.OnStarting): the settings window and the popup show it.
var (
	loadingMu    sync.Mutex
	loadingName  string
	loadingSince time.Time
)

func setLoading(engine string, on bool) {
	loadingMu.Lock()
	defer loadingMu.Unlock()
	if on {
		loadingName, loadingSince = engine, time.Now()
	} else if loadingName == engine {
		loadingName = ""
	}
}

func loadingStatus() map[string]any {
	loadingMu.Lock()
	defer loadingMu.Unlock()
	if loadingName == "" {
		return nil
	}
	return map[string]any{"engine": loadingName, "since": loadingSince}
}
