package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/autostart"
	"voice/internal/core"
	"voice/internal/daemon"
	"voice/internal/engine"
	"voice/internal/engine/httpengine"
	"voice/internal/inject"
	"voice/internal/listen"
	"voice/internal/preset"
	"voice/internal/service"
	"voice/internal/settings"
	"voice/internal/settingsui"
	"voice/internal/tray"
	"voice/sidecar"
)

// newClient reaches this installation's background process. loadConfig must
// have run so the state directory is known.
func newClient(cfgPath string) *daemon.Client {
	exe, _ := os.Executable()
	return &daemon.Client{Exe: exe, ConfigPath: cfgPath, Version: version, InfoPath: daemon.InfoPath(core.DataDir(), exe)}
}

func runServe(args []string) error {
	fs, cfgPath := flags("serve")
	after := fs.Int("after", 0, "wait for this process to exit first (restarts)")
	noTray := fs.Bool("no-tray", false, "do not show the notification area icon")
	fs.Parse(args)
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return err
	}
	c := newClient(*cfgPath)
	switch fs.Arg(0) {
	case "stop":
		return c.Stop(context.Background())
	case "status":
		info, ok := c.Status(context.Background())
		if !ok {
			fmt.Println("not running")
			return nil
		}
		fmt.Printf("running: pid %d, version %s, %s\n", info.PID, info.Version, info.Addr)
		return nil
	case "":
	default:
		return fmt.Errorf("usage: voice serve [stop|status]")
	}

	if *after > 0 {
		daemon.WaitExit(*after, 15*time.Second)
	}
	exe, _ := os.Executable()
	release, err := daemon.SingleInstance(daemon.ID(exe))
	if errors.Is(err, daemon.ErrRunning) {
		fmt.Fprintln(os.Stderr, "voice: already running")
		return nil
	}
	if err != nil {
		return err
	}
	defer release()

	// Started detached, the process has no console; keep a log instead.
	logPath := filepath.Join(core.DataDir(), "serve.log")
	if log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		os.Stderr = log
		defer log.Close()
	}
	logf("voice %s serving (pid %d)", version, os.Getpid())

	core.Logf = logf
	// GPU servers exit after the same idle time as the other models.
	httpengine.SetIdle(int(unloadAfter(cfg).Seconds()))
	sp := core.NewSpeaker(cfg)
	defer sp.Close()
	if cfg.DefaultPreset != "" {
		sp.SetPreset(cfg.DefaultPreset)
	}
	l := listen.New(cfg)
	defer l.Close()
	// Open the device now: Windows lists the session in the volume mixer
	// for as long as the process lives, so the user can set or mute it.
	if err := sp.OpenOutput(); err != nil {
		logf("audio: %v", err)
	}
	// Load the voice and the recognizer now, so the first sentence is not late;
	// right after the PC started (voice runs at login) wait a little, so the
	// models do not slow the rest of the login down. Use before then loads them.
	go func() {
		if up := sinceBoot(); up < 5*time.Minute {
			logf("models: loading in 2 minutes (Windows started %s ago)", up.Round(time.Second))
			time.Sleep(2 * time.Minute)
		}
		sp.Warm()
		if l.Available() {
			l.Warm()
		}
		if isOn(cfg.Serve.PreloadVoices) {
			sp.WarmVoices(voicePresets(cfg))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Sessions and their names survive restarts; ones that ended meanwhile are dropped.
	agents := &service.Agents{Identify: inject.Identity, Names: func() []string {
		var names []string
		for _, ch := range cfg.Characters {
			names = append(names, ch.Name)
		}
		return names
	}}
	agents.Logf = logf
	agents.Load(filepath.Join(core.DataDir(), "agents.json"))
	// Word lists: voice's own next to voice.exe, imported ones in the data folder.
	for _, err := range service.LoadWordLists(filepath.Join(exeDir(), "wordlists"), filepath.Join(core.DataDir(), "wordlists")) {
		logf("word lists: %v", err)
	}
	ctl := startExtras(ctx, cfg, sp, l, agents)
	b := &backend{cfgPath: *cfgPath, cfg: cfg, sp: sp, ctl: ctl, stop: stop, exe: exe, logPath: logPath, devices: cfg.Device}
	ctl.onSpeaking, ctl.onDictating, ctl.onReaderKey = b.speaking, b.dictating, b.toggleReader
	sp.OnPreparing(b.preparing)
	// The Integrations window picks a GPU backend's model size here.
	sidecarOptions = func(name string) map[string]string { return b.cfg.Sidecar[name] }
	engineConfig = func(name string) engine.Config { return b.cfg.Engines[name] }
	saveSidecarOption = func(backend, key, value string) error {
		err := settings.Update(settingsPath(b.cfgPath), func(s *settings.Settings) {
			if s.Sidecar == nil {
				s.Sidecar = map[string]map[string]string{}
			}
			if s.Sidecar[backend] == nil {
				s.Sidecar[backend] = map[string]string{}
			}
			s.Sidecar[backend][key] = value
		})
		if err != nil {
			return err
		}
		_, err = b.reload()
		return err
	}

	svc := &service.Service{Speaker: sp, Config: cfg, Listener: l, Agents: agents, ListenView: ctl.agentListening}
	var once sync.Once
	extra := func(mux *http.ServeMux, info daemon.Info) {
		b.info = info
		settingsui.Register(mux, settingsui.Deps{
			Token: info.Token, SettingsPath: settingsPath(*cfgPath),
			State: b.state, Reload: b.reload, Test: b.test, Restart: b.restart,
			Status: b.panelStatus, Control: b.control, Session: agents.SetSwitches, AvatarDir: filepath.Join(core.DataDir(), "avatars"), ThemesDir: filepath.Join(core.DataDir(), "themes"),
			Character:    func(pid uint32, name string) { agents.SetName(pid, name) },
			Clone:        svc.Clone,
			Integrations: newIntegrations(agents),
			Preview: func(p settings.Preset) string {
				b.cfg.SetPreset(settingsui.PreviewPreset, preset.Preset{Extends: p.Extends, Description: "Preview", Speed: p.Speed, Pitch: p.Pitch, DSP: p.DSP})
				return settingsui.PreviewPreset
			},
		})
		if !*noTray {
			once.Do(func() { go tray.Run(b.trayActions()) })
		}
	}
	deliver := func(text string) string {
		hwnd, pid := inject.Foreground()
		return ctl.deliver(text, hwnd, pid)
	}
	svc.Deliver = deliver
	httpengine.OnStarting = func(engine string, on bool) {
		setLoading(engine, on)
		b.speaking(on) // the popup shows the wait too; speech that follows keeps it up
	}
	svc.DeliverTo = func(pid uint32, text string) (string, bool) {
		hwnd, fg := inject.Foreground()
		r, ok := ctl.deliverTo(pid, text, hwnd, fg, false)
		return outcome(r), ok // the short form the transcript shows
	}
	err = daemon.Serve(ctx, svc, c.InfoPath, version, exe, extra)
	if !*noTray {
		tray.Close()
	}
	return err
}

// backend joins the tray and the settings window to the running process.
type backend struct {
	cfgPath string
	cfg     *preset.Config
	sp      *core.Speaker
	ctl     *controller
	stop    context.CancelFunc
	exe     string
	logPath string
	info    daemon.Info
	devices string // the output device this process opened
}

func (b *backend) save(fn func(*settings.Settings)) {
	if err := settings.Update(settingsPath(b.cfgPath), fn); err != nil {
		logf("settings: %v", err)
	}
}

func (b *backend) trayActions() tray.Actions {
	return tray.Actions{
		State: func() tray.State {
			st := b.sp.Status()
			duck := b.ctl.duckLevel()
			return tray.State{
				Preset: st.Preset, Presets: b.cfg.PresetNames(), Muted: st.Muted, Speaking: st.Speaking,
				Ducking: duck < 1, BargeIn: b.cfg.Serve.BargeIn != nil && *b.cfg.Serve.BargeIn,
				Autostart: autostart.Enabled(),
				Sessions:  sessionLines(b.ctl.liveAgents()),
				Note:      strings.Join(b.ctl.hotkeyErrors(), "; "),
			}
		},
		SetPreset: func(name string) {
			if b.sp.SetPreset(name) == nil {
				b.cfg.DefaultPreset = name
				b.save(func(s *settings.Settings) { s.DefaultPreset = settings.String(name) })
			}
		},
		SetMuted: b.sp.SetMuted,
		Stop:     b.sp.Stop,
		SetDucking: func(on bool) {
			level := 1.0
			if on {
				level = 0.35
				if s, err := settings.Read(settingsPath(b.cfgPath)); err == nil && s.Serve.Duck != nil && *s.Serve.Duck < 1 {
					level = *s.Serve.Duck
				}
			}
			b.cfg.Serve.Duck = &level
			b.ctl.apply()
			b.save(func(s *settings.Settings) { s.Serve.Duck = settings.Float(level) })
		},
		SetBargeIn: func(on bool) {
			b.cfg.Serve.BargeIn = &on
			b.ctl.apply()
			b.save(func(s *settings.Settings) { s.Serve.BargeIn = settings.Bool(on) })
		},
		SetAutostart: b.setAutostart,
		Settings:     b.showPanel,
		Characters:   b.showCharacters,
		Tapped:       b.togglePanel,
		OpenFolder:   func() { exec.Command("explorer.exe", filepath.Dir(settingsPath(b.cfgPath))).Start() },
		OpenLog:      func() { exec.Command("notepad.exe", b.logPath).Start() },
		Restart:      b.restart,
		Quit:         b.stop,
	}
}

// setAutostart adds or removes the login shortcut to this installation's voice.exe.
func (b *backend) setAutostart(on bool) {
	if err := autostart.Set(on, b.exe); err != nil {
		logf("autostart: %v", err)
	}
}

// restart starts a fresh process that takes over once this one exits.
func (b *backend) restart() {
	if err := daemon.StartDetached(b.exe, "serve", "-config", b.cfgPath, "-after", strconv.Itoa(os.Getpid())); err != nil {
		logf("restart: %v", err)
		return
	}
	time.Sleep(200 * time.Millisecond)
	b.stop()
}

func (b *backend) test(name, text string) error {
	if text == "" {
		text = "Hi, this is how I sound."
	}
	_, err := b.sp.Speak(core.Request{Text: text, Preset: name})
	return err
}

// reload re-reads config.toml and settings.toml and applies what can change
// live; devices and the recognizer need a restart.
func (b *backend) reload() (bool, error) {
	next, err := loadConfig(b.cfgPath, "")
	if err != nil {
		return false, err
	}
	cur := b.cfg
	// The speakers are opened once, at start; the microphone (opened for
	// each recording) and the recognizer switch live.
	restart := next.Device != b.devices
	if b.ctl != nil && b.ctl.l != nil {
		b.ctl.l.SetModel(next.Listen.Model, next.Listen.Language, next.Listen.Threads)
		b.ctl.l.SetInputDevice(next.Listen.InputDevice)
		b.ctl.l.SetLanguages(next.Languages)
	}
	// The windows show what is saved, a device waiting for the restart too,
	// and dictation uses the words as saved.
	cur.Device, cur.Listen, cur.Languages = next.Device, next.Listen, next.Languages
	// A GPU backend's option (qwen3's model size) applies from the next
	// request: its server is started again with it.
	for _, sb := range sidecar.Backends() {
		if sb.Option != nil {
			httpengine.SetOptions(sb.Name, map[string]string{sb.Option.Key: sb.Value(next.Sidecar[sb.Name])})
		}
	}
	cur.Sidecar = next.Sidecar
	httpengine.SetIdle(int(unloadAfter(next).Seconds()))
	if isOn(next.Serve.PreloadVoices) && !isOn(cur.Serve.PreloadVoices) {
		defer b.sp.WarmVoices(voicePresets(next)) // turned on: load them now
	}
	cur.DefaultLang, cur.Volume, cur.Serve, cur.Hooks = next.DefaultLang, next.Volume, next.Serve, next.Hooks
	cur.Characters = next.Characters
	// Compare with the active preset too: voice_control set_preset switches it
	// without touching default_preset, and the page shows the active one.
	if next.DefaultPreset != cur.DefaultPreset || next.DefaultPreset != b.sp.Status().Preset {
		if err := b.sp.SetPreset(next.DefaultPreset); err != nil {
			return restart, err
		}
		cur.DefaultPreset = next.DefaultPreset
	}
	b.ctl.apply()
	return restart, nil
}

func (b *backend) state() map[string]any {
	cfg := b.cfg
	// Each preset with what the windows tune: a tuned one (voice-...) also
	// says which preset it extends.
	presets := []map[string]any{}
	for _, name := range cfg.PresetNames() {
		if name == settingsui.PreviewPreset {
			continue
		}
		if r, err := cfg.Resolve(name); err == nil {
			p := map[string]any{"name": name, "description": r.Description, "engine": r.Engine, "speed": r.Speed, "pitch": r.Pitch, "dsp": r.DSP}
			if strings.HasPrefix(name, settings.TunedPrefix) {
				p["extends"] = cfg.PresetExtends(name)
			}
			presets = append(presets, p)
		}
	}
	outputs, _ := audio.Devices()
	inputs, _ := audio.InputDevices()
	volume := 1.0
	if cfg.Volume != nil {
		volume = *cfg.Volume
	}
	duck := b.ctl.duckLevel()
	orNone := func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}
	unload := cfg.Serve.UnloadAfter
	if unload == "" {
		unload = "15m"
	}
	boolOr := func(p *bool, def bool) bool {
		if p == nil {
			return def
		}
		return *p
	}
	return map[string]any{
		"version":        version,
		"default_preset": b.sp.Status().Preset,
		"default_lang":   cfg.DefaultLang,
		"volume":         volume,
		"device":         cfg.Device,
		"serve": map[string]any{
			"duck": duck, "stop_hotkey": orNone(cfg.Serve.StopHotkey), "mute_hotkey": orNone(cfg.Serve.MuteHotkey),
			"barge_in": boolOr(cfg.Serve.BargeIn, false), "unload_after": unload, "preload_voices": boolOr(cfg.Serve.PreloadVoices, false), "corner_speaking": boolOr(cfg.Serve.CornerSpeaking, true),
			"dictate_hotkey": orNone(cfg.Serve.DictateHotkey), "dictate_submit": boolOr(cfg.Serve.DictateSubmit, false),
			"dictate_target": cfg.Serve.DictateTarget, "dictate_feedback": cfg.Serve.DictateFeedback, "dictate_routing": boolOr(cfg.Serve.DictateRouting, false), "dictate_theme": cfg.Serve.DictateTheme, "dictate_overlay_scale": floatOr(cfg.Serve.DictateOverlayScale, 1.5), "personas": boolOr(cfg.Serve.Personas, false), "speaking_popup": boolOr(cfg.Serve.SpeakingPopup, false), "popup_linger": floatOr(cfg.Serve.PopupLinger, 8), "reader_appear": cfg.Serve.Appear(), "reader_hotkey": orNone(cfg.Serve.ReaderHotkey), "reader_theme": derefOr(cfg.Serve.ReaderTheme, ""), "reader_story_theme": derefOr(cfg.Serve.ReaderStoryTheme, ""), "popup_dictation": boolOr(cfg.Serve.PopupDictation, false), "voice_replies": boolOr(cfg.Serve.VoiceReplies, false),
		},
		"listen":     map[string]any{"model": cfg.Listen.Model, "input_device": cfg.Listen.InputDevice, "words": cfg.Listen.Words},
		"languages":  cfg.Languages,
		"characters": cfg.Characters, // who sessions become; the settings window edits the list
		"wordlists":  wordListsState(cfg),
		"transcript": b.ctl.agents.Transcript(), // what voice said and what the user dictated, oldest first
		"hooks": map[string]any{
			"enabled": boolOr(cfg.Hooks.Enabled, true), "summary": boolOr(cfg.Hooks.Summary, true),
			"announce_project": boolOr(cfg.Hooks.AnnounceProject, true), "preset": cfg.Hooks.Preset,
			"summary_sentences": cfg.Hooks.SummaryLength(), "summary_from": cfg.Hooks.SummaryPart(),
			"codex_goal_wait": cfg.Hooks.GoalWait().Seconds(),
		},
		"options": map[string]any{"presets": presets, "outputs": outputs, "inputs": inputs, "models": asrModels(), "themes": overlayThemes(), "traits": cfg.TraitChoices(), "clone_engines": cloneEngines()},
		"notes":   b.ctl.hotkeyErrors(),
		// The sessions dictation can reach: name, client, project, busy, waiting messages.
		"agents": b.ctl.liveAgents(),
	}
}

// asrModels lists installed speech recognition models.
func asrModels() []map[string]string {
	entries, _ := os.ReadDir(engine.Dirs.Models)
	var out []map[string]string
	for _, e := range entries {
		n := e.Name()
		label := ""
		switch {
		case strings.Contains(n, "parakeet"):
			label = "Parakeet (fast, European languages)"
		case strings.Contains(n, "whisper"):
			label = "Whisper (many languages, slower)"
		case strings.Contains(n, "sense-voice"):
			label = "SenseVoice"
		default:
			continue
		}
		out = append(out, map[string]string{"dir": n, "label": label + " — " + n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["dir"] < out[j]["dir"] })
	return out
}

func floatOr(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

// wordListsState is each word list as the settings window shows it, with
// whether it is on.
func wordListsState(cfg *preset.Config) []map[string]any {
	on := map[string]bool{}
	for _, w := range service.ListsOn(cfg.Listen.Lists, cfg.Languages) {
		on[w.ID] = true
	}
	out := []map[string]any{}
	for _, w := range service.WordLists() {
		out = append(out, map[string]any{"id": w.ID, "title": w.Title, "about": w.About, "languages": w.Languages,
			"words": w.Count(), "imported": w.Imported, "on": on[w.ID]})
	}
	return out
}

// voicePresets are the presets voice speaks with: the default one and each
// character's.
func voicePresets(cfg *preset.Config) []string {
	out := []string{cfg.DefaultPreset}
	for _, ch := range cfg.Characters {
		if ch.Preset != "" {
			out = append(out, ch.Preset)
		}
	}
	return out
}

// isOn reads a switch that is off when unset.
func isOn(p *bool) bool { return p != nil && *p }
