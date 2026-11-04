package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"voice/internal/core"
	"voice/internal/flyout"
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
	if popup != nil && b.panel().Visible() {
		popup.Hide()
	}
}

func (b *backend) panelStatus() map[string]any {
	st := b.sp.Status()
	// The line being said, with who says it (from the transcript), so the
	// window fills it in as it is heard; the transcript's newest lines too.
	transcript := b.ctl.agents.Transcript()
	out := map[string]any{"preset": st.Preset, "muted": st.Muted, "speaking": st.Speaking, "transcript": transcript, "agents": b.ctl.liveAgents()}
	if d := b.ctl.Dictation(); d != nil {
		out["dictation"] = d
	}
	if l := st.Line; l != nil {
		line := map[string]any{"id": l.ID, "text": l.Text, "done": l.Done, "from": l.From, "to": l.To, "at": l.At, "seconds": l.Seconds}
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
	if sv := b.cfg.Serve; sv.SpeakingPopup == nil || !*sv.SpeakingPopup || b.panel().Visible() {
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
	if sv := b.cfg.Serve; on && sv.PopupDictation != nil && *sv.PopupDictation && !b.panel().Visible() {
		if err := b.popup().ShowPassive(); err != nil {
			logf("speaking popup: %v", err)
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
	if popup == nil || popupSpeaking || popupDictating {
		return
	}
	popupHide = time.AfterFunc(b.cfg.Serve.Linger(), popup.Hide)
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
