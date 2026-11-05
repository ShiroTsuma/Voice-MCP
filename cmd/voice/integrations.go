package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"voice/internal/engine/openaispeech"

	"github.com/BurntSushi/toml"

	"voice/internal/core"
	"voice/internal/engine"
	"voice/internal/engine/httpengine"
	"voice/internal/integrate"
	"voice/internal/integrations"
	"voice/internal/models"
	"voice/internal/pyruntime"
	"voice/internal/service"
	"voice/sidecar"
)

// Every integration in the Integrations window is a TOML file: the ones
// voice ships in integrations\ next to voice.exe, and any the user drops
// into <data>\integrations (the same id replaces a shipped one). A file
// says what the integration is and how it works: with `uses`, one of the
// handlers below does the work that needs code (editing Claude Code's
// settings, downloading a model); without it, the file's own check and
// commands do. See integrations\README.md.

// integrationFile is one file.
type integrationFile struct {
	ID    string `toml:"id"`
	Group string `toml:"group"`
	Title string `toml:"title"`
	About string `toml:"about"`
	Size  string `toml:"size"`
	Link  string `toml:"link"`
	Order int    `toml:"order"` // within its group, lower first

	Uses     string   `toml:"uses"`     // a handler: claude-hooks, claude-mod, codex, session-api, model, sidecar, python, builtin
	Requires []string `toml:"requires"` // integrations installed first (their ids)
	Model    string   `toml:"model"`    // uses = "model": the name in `voice models`
	Backend  string   `toml:"backend"`  // uses = "sidecar": a name in sidecar/backends.json
	Engine   string   `toml:"engine"`   // uses = "speech-server": the engine (an [engines.<name>] of type openai)
	Guide    string   `toml:"guide"`    // uses = "speech-server": how to set its server up (a web page), offered as an action
	// uses = "speech-server": commands that start and stop its server (Start
	// server, Stop server); the start runs on in the background.
	Start []string `toml:"start"`
	Stop  []string `toml:"stop"`
	// Also: other groups it shows in. Languages: what it speaks or
	// recognizes (en, pl, ...; "*" for nearly all); a sidecar backend's come
	// from sidecar/backends.json when the file names none.
	Also      []string `toml:"also"`
	Languages []string `toml:"languages"`

	Check struct {
		Path    string   `toml:"path"`    // installed when this exists (relative to the file's folder)
		Command []string `toml:"command"` // or when this exits 0
	} `toml:"check"`
	Actions []fileAction `toml:"actions"`

	path string
}

type fileAction struct {
	ID      string   `toml:"id"`
	Label   string   `toml:"label"`
	When    string   `toml:"when"` // installed, missing or always (default)
	Long    bool     `toml:"long"`
	Primary bool     `toml:"primary"`
	Command []string `toml:"command"`
}

// handler does an integration's work in code.
type handler struct {
	status func(f integrationFile) integrations.Status
	do     func(ctx context.Context, f integrationFile, action string, log func(string)) error
}

// newIntegrations loads the integration files and gives each its handler.
func newIntegrations(agents *service.Agents) *integrations.Registry {
	handlers := builtinHandlers(agents)
	files := map[string]integrationFile{}
	var order []string
	for _, dir := range []string{filepath.Join(exeDir(), "integrations"), filepath.Join(core.DataDir(), "integrations")} {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		sort.Strings(paths)
		for _, p := range paths {
			f := readIntegrationFile(p)
			if _, seen := files[f.ID]; !seen {
				order = append(order, f.ID)
			}
			files[f.ID] = f
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return files[order[a]].Order < files[order[b]].Order })
	r := &integrations.Registry{}
	for _, id := range order {
		r.Add(makeIntegration(files[id], handlers))
	}
	return r
}

func readIntegrationFile(path string) integrationFile {
	var f integrationFile
	_, err := toml.DecodeFile(path, &f)
	base := strings.TrimSuffix(filepath.Base(path), ".toml")
	f.path = path
	f.ID, f.Title, f.Group = dirOr(f.ID, base), dirOr(f.Title, base), dirOr(f.Group, "agents")
	if err != nil {
		f.Uses, f.About = "broken", err.Error()
	}
	return f
}

func makeIntegration(f integrationFile, handlers map[string]handler) integrations.Integration {
	it := integrations.Integration{ID: f.ID, Group: f.Group, Title: f.Title, About: f.About, Size: f.Size, Link: f.Link, Requires: f.Requires, Also: f.Also, Languages: f.Languages}
	if b, ok := sidecar.Find(f.Backend); ok && f.Uses == "sidecar" && len(it.Languages) == 0 {
		it.Languages = b.Languages
	}
	// What a voice engine does with voice's tags and moods, in one form.
	m, _ := models.Find(f.Model)
	voiceEngine := f.Uses == "sidecar" || f.Uses == "builtin" || f.Uses == "speech-server" || (f.Uses == "model" && !m.Runtime)
	if (f.Group == "speech" || f.Group == "gpu") && voiceEngine {
		var x core.Expressive
		if b, ok := sidecar.Find(f.Backend); ok && f.Uses == "sidecar" {
			x = core.ParseExpressive(b.Expressive)
		} else if f.Uses == "speech-server" && engineConfig != nil {
			x = core.ParseExpressive(engineConfig(f.Engine)["expressive"])
		} else {
			x = core.ParseExpressive(nil)
		}
		it.Expressive = &integrations.Expressive{Sounds: x.SoundNames(), Emotion: x.Emotion, Moods: x.MoodNames()}
	}
	if f.Uses == "model" && it.Size == "" {
		if m, ok := models.Find(f.Model); ok {
			it.Size = fmt.Sprintf("%d MB", m.SizeMB)
		}
	}
	switch h, ok := handlers[f.Uses]; {
	case f.Uses == "broken":
		msg := f.About
		it.About = f.path
		it.Status = func() integrations.Status {
			return integrations.Status{State: integrations.Error, Detail: "Cannot read the file: " + msg}
		}
	case f.Uses != "" && !ok:
		it.Status = func() integrations.Status {
			return integrations.Status{State: integrations.Error, Detail: fmt.Sprintf("%s: unknown uses = %q", filepath.Base(f.path), f.Uses)}
		}
	case ok:
		it.Status = func() integrations.Status { return h.status(f) }
		if h.do != nil {
			it.Do = func(ctx context.Context, action string, log func(string)) error { return h.do(ctx, f, action, log) }
		}
	default:
		it.Status = func() integrations.Status { return fileStatus(f) }
		it.Do = func(ctx context.Context, action string, log func(string)) error { return fileDo(ctx, f, action, log) }
	}
	return it
}

// fileStatus and fileDo run an integration that its file alone describes.
func fileStatus(f integrationFile) integrations.Status {
	folder := filepath.Dir(f.path)
	on := f.Check.Path == "" && len(f.Check.Command) == 0 // nothing to check: it is there
	if p := f.Check.Path; p != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(folder, p)
		}
		_, err := os.Stat(p)
		on = err == nil
	}
	if !on && len(f.Check.Command) > 0 {
		cmd := exec.Command(f.Check.Command[0], f.Check.Command[1:]...)
		cmd.Dir = folder
		hidden(cmd)
		on = cmd.Run() == nil
	}
	st := integrations.Status{State: integrations.Missing, Detail: "From " + filepath.Base(f.path)}
	if on {
		st.State = integrations.On
	}
	for _, a := range f.Actions {
		if a.When == "installed" && !on || a.When == "missing" && on || len(a.Command) == 0 {
			continue
		}
		st.Actions = append(st.Actions, integrations.Action{ID: a.ID, Label: dirOr(a.Label, a.ID), Long: a.Long, Primary: a.Primary})
	}
	return st
}

func fileDo(ctx context.Context, f integrationFile, action string, log func(string)) error {
	for _, a := range f.Actions {
		if a.ID == action && len(a.Command) > 0 {
			log("> " + strings.Join(a.Command, " "))
			cmd := exec.CommandContext(ctx, a.Command[0], a.Command[1:]...)
			cmd.Dir = filepath.Dir(f.path)
			return runLogged(cmd, log)
		}
	}
	return fmt.Errorf("no command for %s", action)
}

// builtinHandlers are the integrations that need code.
func builtinHandlers(agents *service.Agents) map[string]handler {
	exe, _ := os.Executable()
	codex, claude, _ := clientHomes()
	onOff := func(on bool, detail string) integrations.Status {
		if on {
			return integrations.Status{State: integrations.On, Detail: detail, Actions: []integrations.Action{{ID: "off", Label: "Turn off"}}}
		}
		return integrations.Status{State: integrations.Off, Detail: detail, Actions: []integrations.Action{{ID: "on", Label: "Turn on", Primary: true}}}
	}
	note := func(changed bool, err error, log func(string), what string) error {
		if err == nil && changed {
			log(what)
		}
		return err
	}
	claudeSettings := filepath.Join(claude, "settings.json")
	codexConfig, codexHooks := filepath.Join(codex, "config.toml"), filepath.Join(codex, "hooks.json")

	return map[string]handler{
		"builtin": {status: func(integrationFile) integrations.Status {
			return integrations.Status{State: integrations.On, Detail: "Built in"}
		}},

		// A speech server voice does not start (OpenAI speech API): it is set
		// up when it answers.
		"speech-server": {status: func(f integrationFile) integrations.Status {
			url := ""
			if engineConfig != nil {
				url = engineConfig(f.Engine).String("url")
			}
			if url == "" {
				return integrations.Status{State: integrations.Error, Detail: "No [engines." + f.Engine + "] with a url in the config"}
			}
			var acts []integrations.Action
			add := func(id, label string, primary bool) {
				acts = append(acts, integrations.Action{ID: id, Label: label, Primary: primary})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			if err := openaispeech.Probe(ctx, url); err == nil {
				if len(f.Stop) > 0 {
					add("stop", "Stop server", false)
				}
				if f.Guide != "" {
					add("guide", "Setup guide", false)
				}
				return integrations.Status{State: integrations.On, Detail: "Its server answers at " + url + "; it keeps the graphics card's memory until stopped", Actions: acts}
			}
			if since, ok := serverStarting(f.ID); ok {
				if len(f.Stop) > 0 {
					add("stop", "Stop server", false)
				}
				return integrations.Status{State: integrations.Partly, Detail: fmt.Sprintf("Starting for %s; the first start tunes the graphics card for over 10 minutes, later ones take a few", since.Round(time.Second)), Actions: acts}
			}
			if len(f.Start) > 0 {
				add("start", "Start server", true)
			}
			if f.Guide != "" {
				add("guide", "Setup guide", len(f.Start) == 0)
			}
			return integrations.Status{State: integrations.Missing, Detail: "Its server does not answer at " + url + ". Set one up (Setup guide) on this PC, another machine or a server, and set its url in config.toml; until then another voice speaks", Actions: acts}
		},
			do: func(_ context.Context, f integrationFile, action string, log func(string)) error {
				switch {
				case action == "guide" && f.Guide != "":
					log("Opening " + f.Guide)
					return exec.Command("rundll32", "url.dll,FileProtocolHandler", f.Guide).Start() // the default browser
				case action == "start" && len(f.Start) > 0:
					// It runs on after the action: the server lives until stopped,
					// so it starts in the user's folder, not voice's own (a process
					// working in that would keep an update from replacing it).
					cmd := exec.Command(f.Start[0], f.Start[1:]...)
					if home, err := os.UserHomeDir(); err == nil {
						cmd.Dir = home
					}
					hidden(cmd)
					if err := cmd.Start(); err != nil {
						return err
					}
					markStarting(f.ID, cmd)
					log("Started: " + strings.Join(f.Start, " "))
					log("It answers once loaded; the first start takes over 10 minutes.")
					return nil
				case action == "stop" && len(f.Stop) > 0:
					cmd := exec.Command(f.Stop[0], f.Stop[1:]...)
					cmd.Dir = filepath.Dir(f.path)
					hidden(cmd)
					out, err := cmd.CombinedOutput()
					if s := strings.TrimSpace(string(out)); s != "" {
						log(s)
					}
					stopStarting(f.ID)
					return err
				}
				return fmt.Errorf("%s has no action %q", f.Title, action)
			}},

		"claude-hooks": {
			status: func(integrationFile) integrations.Status {
				if claude == "" {
					return integrations.Status{State: integrations.Missing, Detail: "Claude Code is not installed (no ~/.claude folder)"}
				}
				return onOff(integrate.HasHooks(claudeSettings), "In "+claudeSettings)
			},
			do: func(_ context.Context, _ integrationFile, action string, log func(string)) error {
				if action == "on" {
					changed, err := integrate.Claude(claudeSettings, exe)
					return note(changed, err, log, "Hooks added; new Claude Code sessions use them")
				}
				changed, err := integrate.RemoveClaude(claudeSettings)
				return note(changed, err, log, "Hooks removed")
			},
		},

		"claude-mod": {
			status: func(integrationFile) integrations.Status {
				if claude == "" {
					return integrations.Status{State: integrations.Missing, Detail: "Claude Code is not installed"}
				}
				if _, err := exec.LookPath("claude"); err != nil {
					return integrations.Status{State: integrations.Partly, Detail: "Needs the claude command on PATH; see claude-plugin\\voice-dictation\\README.md to install it by hand"}
				}
				return onOff(claudePluginOn(claude), "voice@voice at user scope; new sessions load it")
			},
			do: func(_ context.Context, _ integrationFile, action string, log func(string)) error {
				claudeModTo(action == "on", log)
				return nil
			},
		},

		"codex": {
			status: func(integrationFile) integrations.Status {
				if _, err := os.Stat(codexConfig); err != nil {
					return integrations.Status{State: integrations.Missing, Detail: "Codex is not installed (no ~/.codex/config.toml)"}
				}
				notify, hooks, skill := integrate.HasCodexNotify(codexConfig), integrate.HasHooks(codexHooks), integrate.HasCodexSkill(codex)
				switch {
				case notify && hooks && skill:
					return onOff(true, "Trust the hooks once with /hooks in Codex; it asks again when a voice update changes them")
				case notify || hooks || skill:
					st := onOff(false, fmt.Sprintf("Partly set up: notify %s, hooks %s, $character %s", yesNo(notify), yesNo(hooks), yesNo(skill)))
					st.State = integrations.Partly
					st.Actions = append(st.Actions, integrations.Action{ID: "off", Label: "Remove"})
					return st
				}
				return onOff(false, "")
			},
			do: func(_ context.Context, _ integrationFile, action string, log func(string)) error {
				if action == "on" {
					if changed, err := integrate.Codex(codexConfig, "voice", exe); err != nil {
						return err
					} else if changed {
						log("notify and the tool timeout set in " + codexConfig)
					}
					if changed, err := integrate.CodexHooks(codexHooks, exe); err != nil {
						return err
					} else if changed {
						log("hooks set in " + codexHooks + "; trust them with /hooks in Codex")
					}
					changed, err := integrate.CodexSkill(codex)
					return note(changed, err, log, "the $character skill added")
				}
				if changed, err := integrate.RemoveCodex(codexConfig); err != nil {
					return err
				} else if changed {
					log("notify restored")
				}
				if changed, err := integrate.RemoveCodexHooks(codexHooks); err != nil {
					return err
				} else if changed {
					log("hooks removed")
				}
				changed, err := integrate.RemoveCodexSkill(codex)
				return note(changed, err, log, "the $character skill removed")
			},
		},

		"session-api": {status: func(integrationFile) integrations.Status {
			var names []string
			for _, a := range agents.Info() {
				if k := agents.Client(a.PID); k != "claude" && k != "codex" {
					names = append(names, a.Name+" ("+k+")")
				}
			}
			if len(names) == 0 {
				return integrations.Status{State: integrations.On, Detail: "Always on; no agent is connected now"}
			}
			return integrations.Status{State: integrations.On, Detail: "Connected: " + strings.Join(names, ", ")}
		}},

		"model": {
			status: func(f integrationFile) integrations.Status {
				m, ok := models.Find(f.Model)
				if !ok {
					return integrations.Status{State: integrations.Error, Detail: fmt.Sprintf("no model %q in `voice models`", f.Model)}
				}
				if models.Installed(m, engine.Dirs.Models, engine.Dirs.Runtime) {
					st := integrations.Status{State: integrations.On, Detail: "Downloaded"}
					if m.Dir != "" { // the runtime has no folder of its own to remove
						st.Actions = []integrations.Action{{ID: "remove", Label: "Remove"}}
					}
					return st
				}
				return integrations.Status{State: integrations.Missing, Actions: []integrations.Action{{ID: "download", Label: "Download", Long: true, Primary: true}}}
			},
			do: func(ctx context.Context, f integrationFile, action string, log func(string)) error {
				m, _ := models.Find(f.Model)
				if action == "remove" {
					dir := filepath.Join(engine.Dirs.Models, m.Dir)
					log("Removing " + dir)
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
					log("Removed; Download brings it back")
					return nil
				}
				log(fmt.Sprintf("Downloading %s (%d MB)…", m.Name, m.SizeMB))
				last := -1
				err := models.Pull(ctx, m, engine.Dirs.Models, engine.Dirs.Runtime, func(done, total int64) {
					if total > 0 {
						if p := int(done * 100 / total); p/10 != last/10 {
							last = p
							log(fmt.Sprintf("%d%%", p))
						}
					}
				})
				if err == nil {
					log("Done; voice uses it from the next request")
				}
				return err
			},
		},

		"python": {
			status: func(integrationFile) integrations.Status {
				if pyruntime.Installed(httpengine.SidecarDir) {
					return integrations.Status{State: integrations.On, Detail: "Python " + pyruntime.Version + " in " + pyruntime.Dir(httpengine.SidecarDir),
						Actions: []integrations.Action{{ID: "remove", Label: "Remove"}}}
				}
				return integrations.Status{State: integrations.Missing, Actions: []integrations.Action{{ID: "install", Label: "Download", Long: true, Primary: true}}}
			},
			do: func(ctx context.Context, _ integrationFile, action string, log func(string)) error {
				if action == "remove" {
					log("Removing " + pyruntime.Dir(httpengine.SidecarDir))
					return os.RemoveAll(pyruntime.Dir(httpengine.SidecarDir))
				}
				log(fmt.Sprintf("Downloading Python %s (%d MB) from python-build-standalone, checked against its pinned SHA-256…", pyruntime.Version, pyruntime.SizeMB))
				last := -1
				err := pyruntime.Install(ctx, httpengine.SidecarDir, func(done, total int64) {
					if total > 0 {
						if p := int(done * 100 / total); p/20 != last/20 {
							last = p
							log(fmt.Sprintf("%d%%", p))
						}
					}
				})
				if err == nil {
					log("Python is ready in " + pyruntime.Dir(httpengine.SidecarDir))
				}
				return err
			},
		},

		"sidecar": {
			status: func(f integrationFile) integrations.Status {
				st := sidecarStatus(f.Backend)
				st.Choice = sidecarChoice(f.Backend)
				return st
			},
			do: func(ctx context.Context, f integrationFile, action string, log func(string)) error {
				if value, ok := strings.CutPrefix(action, integrations.ChoosePrefix); ok {
					b, _ := sidecar.Find(f.Backend)
					if b.Option == nil || saveSidecarOption == nil {
						return fmt.Errorf("%s has nothing to choose here", f.Title)
					}
					return saveSidecarOption(b.Name, b.Option.Key, value)
				}
				if action == "load" || action == "unload" {
					e, ok := gpuEngine(f.Backend)
					if !ok {
						return fmt.Errorf("%s is not available in this voice", f.Title)
					}
					if action == "unload" {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						e.Unload(ctx)
						log("Unloaded: the graphics card is free; the next sentence in its voice loads it again")
						return nil
					}
					log("Loading " + f.Title + " on the graphics card; the first time can take minutes")
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
					defer cancel()
					if err := e.Warm(ctx); err != nil {
						return err
					}
					log("Loaded")
					return nil
				}
				if action == "remove" {
					dir := filepath.Join(httpengine.SidecarDir, "envs", f.Backend)
					log("Removing " + dir)
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
					log("Removed. Model weights the backend downloaded stay in " + filepath.Join(httpengine.SidecarDir, "models"))
					return nil
				}
				log("Setting up " + f.Title + ": a Python environment, PyTorch for this GPU, then the backend. This takes a while.")
				args := []string{"sidecar"}
				if pyruntime.Installed(httpengine.SidecarDir) { // the Python voice brings, when it is there
					args = append(args, "-python", pyruntime.Exe(httpengine.SidecarDir))
				}
				cmd := exec.CommandContext(ctx, exe, append(args, "setup", f.Backend)...)
				return runLogged(cmd, log)
			},
		},
	}
}

// sidecarOptions and saveSidecarOption reach the settings of the voice that
// runs (serve sets them): the chosen options of a GPU backend, and saving one.
var (
	sidecarOptions func(backend string) map[string]string
	// engineConfig is an [engines.<name>] table of the running voice.
	engineConfig      func(name string) engine.Config
	saveSidecarOption func(backend, key, value string) error
)

// sidecarChoice is a GPU backend's option as the window's picker: qwen3's
// model size.
func sidecarChoice(name string) *integrations.Choice {
	b, ok := sidecar.Find(name)
	if !ok || b.Option == nil {
		return nil
	}
	var set map[string]string
	if sidecarOptions != nil {
		set = sidecarOptions(name)
	}
	c := &integrations.Choice{Label: b.Option.Label, Value: b.Value(set)}
	for _, o := range b.Option.Choices {
		c.Options = append(c.Options, integrations.ChoiceOption{Value: o.Value, Label: o.Label, Note: o.Note})
	}
	return c
}

func yesNo(b bool) string { return map[bool]string{true: "yes", false: "no"}[b] }

// claudePluginOn reports whether Claude Code has voice@voice installed and
// enabled.
func claudePluginOn(claudeHome string) bool {
	b, err := os.ReadFile(filepath.Join(claudeHome, "plugins", "installed_plugins.json"))
	if err != nil || !strings.Contains(string(b), `"voice@voice"`) {
		return false
	}
	var s struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	if b, err := os.ReadFile(filepath.Join(claudeHome, "settings.json")); err == nil && json.Unmarshal(b, &s) == nil && s.Enabled != nil {
		if on, ok := s.Enabled["voice@voice"]; ok {
			return on
		}
	}
	return true
}

// sidecarStatus reads what the sidecar setup recorded for a backend. A
// backend in the older shared environment (.venv) works too: the engines use
// it when the backend has no environment of its own.
func sidecarStatus(name string) integrations.Status {
	install := []integrations.Action{{ID: "install", Label: "Install", Long: true, Primary: true}}
	remove := integrations.Action{ID: "remove", Label: "Remove"}
	var st struct {
		Backend bool   `json:"backend"`
		GPU     string `json:"gpu"`
	}
	if b, err := os.ReadFile(filepath.Join(httpengine.SidecarDir, "envs", name, ".voice-setup.json")); err == nil {
		// setup.ps1 writes it with Windows PowerShell, which starts it with a BOM.
		json.Unmarshal(bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")), &st)
		if st.Backend {
			// Loaded or not: voice loads it on first use and frees the graphics
			// card after the idle time; Load now and Unload do it by hand.
			acts := []integrations.Action{{ID: "load", Label: "Load now", Long: true}, {ID: "install", Label: "Reinstall", Long: true}, remove}
			detail := "Installed for " + st.GPU + "; it loads when a voice uses it and frees the graphics card when idle (General, Free idle models)"
			if e, ok := gpuEngine(name); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				loaded := e.Loaded(ctx)
				cancel()
				if loaded {
					acts[0] = integrations.Action{ID: "unload", Label: "Unload"}
					detail = "Loaded on the graphics card (" + st.GPU + "); it frees it when idle, or now with Unload"
				}
			}
			return integrations.Status{State: integrations.On, Detail: detail, Actions: acts}
		}
		return integrations.Status{State: integrations.Partly, Detail: "The setup stopped before it finished; run it again to resume", Actions: append(install, remove)}
	}
	if b, _ := sidecar.Find(name); b.Module != "" {
		pkg := b.Module
		if _, err := os.Stat(filepath.Join(httpengine.SidecarDir, ".venv", "Lib", "site-packages", pkg)); err == nil {
			return integrations.Status{State: integrations.On, Detail: "Works from an older shared environment (from earlier voice versions); Install separately gives it its own",
				Actions: []integrations.Action{{ID: "install", Label: "Install separately", Long: true}}}
		}
	}
	return integrations.Status{State: integrations.Missing, Actions: install}
}

// runLogged runs cmd with a hidden console and sends each line of its output
// to log.
func runLogged(cmd *exec.Cmd, log func(string)) error {
	hidden(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			log(sc.Text())
		}
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

// cloneEngines are the engines the Voice clone group offers, with whether
// each is installed (the Integrations window installs the others).
func cloneEngines() []map[string]any {
	model := func(name string) bool {
		m, ok := models.Find(name)
		return ok && models.Installed(m, engine.Dirs.Models, engine.Dirs.Runtime)
	}
	out := []map[string]any{
		{"name": "pocket", "label": "Pocket", "note": "on the CPU; the default", "ready": model("pocket"), "languages": []string{"en"}},
		{"name": "zipvoice", "label": "ZipVoice", "note": "on the CPU", "ready": model("zipvoice"), "transcript": true, "languages": []string{"en", "zh"}},
	}
	// The GPU backends, from sidecar/backends.json.
	for _, b := range sidecar.Backends() {
		if b.Clones && !b.Hidden {
			out = append(out, map[string]any{"name": b.Name, "label": b.Label, "note": b.Note,
				"ready": sidecarStatus(b.Name).State == integrations.On, "transcript": b.Transcript, "languages": b.Languages})
		}
	}
	return out
}

// Speech servers started from the Integrations window, while they load.
var starting = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func markStarting(id string, cmd *exec.Cmd) {
	starting.Lock()
	starting.at[id] = time.Now()
	starting.Unlock()
	go func() { // the server stopped (or failed): no longer starting
		cmd.Wait()
		stopStarting(id)
	}()
}

func stopStarting(id string) {
	starting.Lock()
	delete(starting.at, id)
	starting.Unlock()
}

// serverStarting reports how long ago id's server was started, while its
// process runs and it does not answer yet.
func serverStarting(id string) (time.Duration, bool) {
	starting.Lock()
	defer starting.Unlock()
	at, ok := starting.at[id]
	return time.Since(at), ok
}

// loadable is a GPU voice's engine that can be loaded and unloaded by hand.
type loadable interface {
	Warm(ctx context.Context) error
	Loaded(ctx context.Context) bool
	Unload(ctx context.Context)
}

// voiceEngine is an engine of the running voice (its speaker's).
var voiceEngine func(name string) (engine.Engine, error)

// gpuEngine is backend name's engine when voice runs it.
func gpuEngine(name string) (loadable, bool) {
	if voiceEngine == nil {
		return nil, false
	}
	e, err := voiceEngine(name)
	if err != nil {
		return nil, false
	}
	l, ok := e.(loadable)
	return l, ok
}
