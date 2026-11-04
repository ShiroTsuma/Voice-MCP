// Package tray shows voice in the notification area: an icon that reflects
// mute, and a menu to switch voices, mute, stop, toggle ducking and
// barge-in, and open the settings window.
package tray

import (
	_ "embed"
	"runtime"
	"time"

	"fyne.io/systray"
)

//go:embed voice.ico
var iconNormal []byte

//go:embed voice-muted.ico
var iconMuted []byte

// State is what the menu shows; the tray polls it.
type State struct {
	Preset   string
	Presets  []string // available presets in menu order
	Muted    bool
	Speaking bool
	Ducking  bool
	BargeIn  bool
	// Autostart: the background process starts at login (Startup folder shortcut).
	Autostart bool
	Note      string // e.g. a hotkey that could not be registered
	// Sessions: one line per session dictation reaches, e.g. "Annie · Claude · MCP-Tools · working".
	Sessions []string
}

// Actions are what the menu items do.
type Actions struct {
	State      func() State
	SetPreset  func(name string)
	SetMuted   func(bool)
	Stop       func()
	SetDucking func(bool)
	SetBargeIn func(bool)
	// SetAutostart is nil where login autostart is not available.
	SetAutostart func(bool)
	// Settings opens the settings window; Tapped (a click on the icon)
	// opens or closes it.
	Settings   func()
	Characters func() // opens the characters window
	Tapped     func()
	OpenFolder func()
	OpenLog    func()
	Restart    func()
	Quit       func()
}

// Run shows the tray icon until Quit is chosen or Close is called. It
// blocks and runs on its own goroutine. systray only pins the main thread
// (in its init), and Windows delivers a window's messages only to the thread
// that created it: without the lock the scheduler can move this goroutine
// and the icon stops answering clicks.
func Run(a Actions) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	systray.Run(func() { ready(a) }, func() {})
}

// Close removes the icon.
func Close() { systray.Quit() }

func ready(a Actions) {
	systray.SetIcon(iconNormal)
	systray.SetTitle("voice")
	systray.SetTooltip("voice")
	systray.SetOnTapped(a.Tapped)

	header := systray.AddMenuItem("voice", "")
	header.Disable()
	note := systray.AddMenuItem("", "")
	note.Disable()
	note.Hide()
	systray.AddSeparator()
	mute := systray.AddMenuItemCheckbox("Mute", "Refuse all speech until unmuted", false)
	stop := systray.AddMenuItem("Stop speaking", "Silence now and drop the queue")
	voices := systray.AddMenuItem("Voice", "Preset used by default")
	sessions := systray.AddMenuItem("Sessions", "Who dictation reaches: say the name first to pick one")
	systray.AddSeparator()
	duck := systray.AddMenuItemCheckbox("Lower other apps while speaking", "", false)
	barge := systray.AddMenuItemCheckbox("Stop when I start talking", "Barge-in; works best with headphones", false)
	login := systray.AddMenuItemCheckbox("Start at login", "Tray icon and hotkeys right after you sign in", false)
	if a.SetAutostart == nil {
		login.Hide()
	}
	systray.AddSeparator()
	settings := systray.AddMenuItem("Settings…", "Hotkeys, devices, voices and more")
	charactersItem := systray.AddMenuItem("Characters…", "Who sessions become: names, personalities, voices, pictures")
	folder := systray.AddMenuItem("Open config folder", "")
	logItem := systray.AddMenuItem("Open log", "")
	systray.AddSeparator()
	restart := systray.AddMenuItem("Restart voice", "Reload configuration and models")
	quit := systray.AddMenuItem("Quit voice", "Stops the background process; it starts again on next use")

	presetItems := map[string]*systray.MenuItem{}
	var sessionItems []*systray.MenuItem // reused; extra ones hidden
	var lastMuted *bool
	refresh := func() {
		st := a.State()
		title := "voice · " + st.Preset
		if st.Muted {
			title += " (muted)"
		} else if st.Speaking {
			title += " (speaking)"
		}
		header.SetTitle(title)
		systray.SetTooltip(title)
		if st.Note != "" {
			note.SetTitle("⚠ " + st.Note)
			note.Show()
		} else {
			note.Hide()
		}
		if lastMuted == nil || *lastMuted != st.Muted {
			if st.Muted {
				systray.SetIcon(iconMuted)
				mute.Check()
			} else {
				systray.SetIcon(iconNormal)
				mute.Uncheck()
			}
			m := st.Muted
			lastMuted = &m
		}
		setCheck(duck, st.Ducking)
		setCheck(barge, st.BargeIn)
		setCheck(login, st.Autostart)
		for _, name := range st.Presets {
			item, ok := presetItems[name]
			if !ok {
				item = voices.AddSubMenuItemCheckbox(name, "", false)
				presetItems[name] = item
				go func(name string, item *systray.MenuItem) {
					for range item.ClickedCh {
						a.SetPreset(name)
					}
				}(name, item)
			}
			setCheck(item, name == st.Preset)
		}
		lines := st.Sessions
		if len(lines) == 0 {
			lines = []string{"No sessions yet"}
		}
		for i, line := range lines {
			if i == len(sessionItems) {
				item := sessions.AddSubMenuItem(line, "")
				item.Disable() // information only
				sessionItems = append(sessionItems, item)
			}
			sessionItems[i].SetTitle(line)
			sessionItems[i].Show()
		}
		for _, item := range sessionItems[len(lines):] {
			item.Hide()
		}
	}
	refresh()

	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
			case <-mute.ClickedCh:
				a.SetMuted(!mute.Checked())
			case <-stop.ClickedCh:
				a.Stop()
			case <-duck.ClickedCh:
				a.SetDucking(!duck.Checked())
			case <-barge.ClickedCh:
				a.SetBargeIn(!barge.Checked())
			case <-login.ClickedCh:
				if a.SetAutostart != nil {
					a.SetAutostart(!login.Checked())
				}
			case <-settings.ClickedCh:
				a.Settings()
			case <-charactersItem.ClickedCh:
				if a.Characters != nil {
					a.Characters()
				}
			case <-folder.ClickedCh:
				a.OpenFolder()
			case <-logItem.ClickedCh:
				a.OpenLog()
			case <-restart.ClickedCh:
				a.Restart()
			case <-quit.ClickedCh:
				a.Quit()
				return
			}
			refresh()
		}
	}()
}

func setCheck(item *systray.MenuItem, on bool) {
	if item.Checked() != on {
		if on {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}
