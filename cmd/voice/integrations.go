package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"voice/internal/core"
	"voice/internal/engine"
	"voice/internal/engine/httpengine"
	"voice/internal/integrate"
	"voice/internal/integrations"
	"voice/internal/models"
	"voice/internal/service"
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

	Uses    string `toml:"uses"`    // a handler: claude-hooks, claude-mod, codex, session-api, model, sidecar, builtin
	Model   string `toml:"model"`   // uses = "model": the name in `voice models`
	Backend string `toml:"backend"` // uses = "sidecar": chatterbox, xtts, f5

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
	it := integrations.Integration{ID: f.ID, Group: f.Group, Title: f.Title, About: f.About, Size: f.Size, Link: f.Link}
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
					return integrations.Status{State: integrations.On, Detail: "Downloaded"}
				}
				return integrations.Status{State: integrations.Missing, Actions: []integrations.Action{{ID: "download", Label: "Download", Long: true, Primary: true}}}
			},
			do: func(ctx context.Context, f integrationFile, _ string, log func(string)) error {
				m, _ := models.Find(f.Model)
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

		"sidecar": {
			status: func(f integrationFile) integrations.Status { return sidecarStatus(f.Backend) },
			do: func(ctx context.Context, f integrationFile, _ string, log func(string)) error {
				log("Setting up " + f.Title + ": a Python environment, PyTorch for this GPU, then the backend. This takes a while.")
				cmd := exec.CommandContext(ctx, exe, "sidecar", "setup", f.Backend)
				return runLogged(cmd, log)
			},
		},
	}
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

// sidecarStatus reads what the sidecar setup recorded for a backend.
func sidecarStatus(name string) integrations.Status {
	install := []integrations.Action{{ID: "install", Label: "Install", Long: true, Primary: true}}
	var st struct {
		Backend bool   `json:"backend"`
		GPU     string `json:"gpu"`
	}
	if b, err := os.ReadFile(filepath.Join(httpengine.SidecarDir, "envs", name, ".voice-setup.json")); err == nil {
		json.Unmarshal(b, &st)
		if st.Backend {
			return integrations.Status{State: integrations.On, Detail: "Installed for " + st.GPU, Actions: []integrations.Action{{ID: "install", Label: "Reinstall", Long: true}}}
		}
		return integrations.Status{State: integrations.Partly, Detail: "The setup stopped before it finished; run it again to resume", Actions: install}
	}
	if _, err := os.Stat(filepath.Join(httpengine.SidecarDir, ".venv")); err == nil {
		return integrations.Status{State: integrations.Partly, Detail: "An older shared environment exists; Install gives the backend its own", Actions: install}
	}
	return integrations.Status{State: integrations.Missing, Detail: "Needs Python 3.12", Actions: install}
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
