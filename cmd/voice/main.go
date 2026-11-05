// Command voice gives AI agents a voice: an MCP server, a CLI for hooks and
// preset tools sharing one engine and playback pipeline.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"voice/internal/audio"
	"voice/internal/clones"
	"voice/internal/core"
	"voice/internal/engine"
	"voice/internal/hooks"
	"voice/internal/mcpserver"
	"voice/internal/models"
	"voice/internal/preset"
	"voice/internal/service"

	"voice/internal/engine/httpengine"

	_ "voice/internal/engine/command"
	_ "voice/internal/engine/sapi"
	_ "voice/internal/engine/sherpa"
)

var version = "dev"

// Builds without -X main.version (the shared CodexTools build) still need a
// version that changes with every build: clients replace a background
// process whose version differs from theirs.
func init() {
	if version != "dev" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		if st, err := os.Stat(exe); err == nil {
			version = fmt.Sprintf("dev-%x-%x", st.ModTime().Unix(), st.Size())
		}
	}
}

const usage = `voice - speech for AI agents

Usage:
  voice mcp [-client c]      run the MCP server on stdio
  voice serve [stop|status]  background process owning audio and models (started on demand)
  voice say [flags] [text]   speak text (or stdin) and wait until it is heard
  voice hook claude|codex    announce a client hook event (for hook configs)
  voice clone add|list|...   manage cloned voices (voice clone for help)
  voice listen               record an answer from the microphone and print it
  voice dictate text         pass text to the agent the way the dictation hotkey does
  voice transcribe file.wav  speech to text for a recording
  voice paths                where models, voices, cache and sidecar live
  voice sidecar setup b,...  install GPU backends (chatterbox, f5, xtts, qwen3)
  voice presets              list voice presets
  voice voices [-engine e]   list voices an engine offers
  voice devices              list audio outputs and microphones (* = selected)
  voice models [pull <name>] list or download neural models and runtime
  voice install [-data-root dir]  set up this folder: MCP server, skills, hooks, mod
  voice uninstall            undo install (keeps the folder)
  voice setup [flags]        installer hooks: -stop dir, -config f -data-root dir, -clients
  voice autostart on|off|status   start the background process at login (off by default)
  voice theme install|remove|list|path   the reading window's themes
  voice version

Common flags:
  -config path   config file (default: VOICE_CONFIG or config.toml next to voice.exe)

Output device: say -device, then VOICE_DEVICE, then device = "..." in config.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "say":
		err = runSay(args)
	case "presets":
		err = runPresets(args)
	case "voices":
		err = runVoices(args)
	case "devices":
		err = runDevices(args)
	case "models":
		err = runModels(args)
	case "mcp":
		err = runMCP(args)
	case "serve":
		err = runServe(args)
	case "hook":
		err = runHook(args)
	case "clone":
		err = runClone(args)
	case "listen":
		err = runListen(args)
	case "dictate":
		err = runDictate(args)
	case "paths":
		err = runPaths(args)
	case "mixer":
		err = runMixer(args)
	case "sidecar":
		err = runSidecar(args)
	case "transcribe":
		err = runTranscribe(args)
	case "setup":
		err = runSetup(args)
	case "install":
		err = runInstall(args)
	case "uninstall":
		err = runUninstall(args)
	case "autostart":
		err = runAutostart(args)
	case "theme":
		err = runTheme(args)
	case "--runtime-info":
		// Distribution checks: where persistent state goes.
		b, _ := json.Marshal(map[string]string{"root": toolDir(), "output": filepath.Join(toolDir(), "output")})
		fmt.Println(string(b))
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "voice: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "voice:", err)
		os.Exit(1)
	}
}

// exeDir holds voice.exe and the files shipped with it.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// toolDir holds durable state: config.toml and, by default, data. In an
// installation voice.exe lives in <tool>\binary-runtime, which updates
// replace, so state belongs one level up.
func toolDir() string {
	dir := exeDir()
	if strings.EqualFold(filepath.Base(dir), "binary-runtime") {
		return filepath.Dir(dir)
	}
	return dir
}

// settingsPath is settings.toml next to the config file: the layer the tray
// and settings window write.
func settingsPath(cfgPath string) string {
	if cfgPath == "" {
		return filepath.Join(toolDir(), "settings.toml")
	}
	return filepath.Join(filepath.Dir(cfgPath), "settings.toml")
}

func defaultConfigPath() string {
	if p := os.Getenv("VOICE_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(toolDir(), "config.toml")
}

func flags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfg := fs.String("config", defaultConfigPath(), "config file")
	return fs, cfg
}

func runSay(args []string) error {
	fs, cfgPath := flags("say")
	presetName := fs.String("preset", os.Getenv("VOICE_PRESET"), "voice preset")
	intent := fs.String("intent", core.IntentInfo, "info, attention or question")
	lang := fs.String("lang", "", "en, pl, ... or auto to detect (default: default_lang from config, en)")
	timeout := fs.Duration("timeout", 2*time.Minute, "give up after this long")
	device := fs.String("device", "", "output device name substring (overrides config and VOICE_DEVICE)")
	out := fs.String("out", "", "write a WAV file instead of playing")
	verbose := fs.Bool("v", false, "report which engine spoke")
	cache := fs.Bool("cache", false, "reuse rendered audio for this exact phrase")
	fs.Parse(args)

	text := strings.Join(fs.Args(), " ")
	if text == "" || text == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	// Normal speech goes through the background process, so it shares the
	// queue, the mixer entry and loaded models. Rendering to a file or to a
	// one-off device runs here.
	if *out == "" && *device == "" {
		if _, err := loadConfig(*cfgPath, ""); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		res, err := newClient(*cfgPath).Speak(ctx, service.SpeakArgs{Text: text, Intent: *intent, Lang: *lang, Preset: *presetName, Cache: *cache, Wait: true})
		if err != nil {
			return err
		}
		if m, _ := res.(map[string]any); *verbose && m != nil {
			fmt.Fprintf(os.Stderr, "spoken by engine %v\n", m["engine"])
		}
		return nil
	}
	cfg, err := loadConfig(*cfgPath, *device)
	if err != nil {
		return err
	}
	sp := core.NewSpeaker(cfg)
	defer sp.Close()
	if *out != "" {
		sp.RecordTo(*out)
	}
	t, err := sp.Speak(core.Request{Text: text, Intent: *intent, Lang: *lang, Preset: *presetName, Cache: *cache})
	if err != nil {
		return err
	}
	select {
	case res := <-t.Done:
		if res.Err != nil {
			return res.Err
		}
		if *verbose {
			fmt.Fprintf(os.Stderr, "spoken by engine %s\n", res.Engine)
		}
	case <-time.After(*timeout):
		return fmt.Errorf("timed out after %s", *timeout)
	}
	sp.Close()
	if msg := sp.Err(); *out != "" && msg != "" {
		return errors.New(msg)
	}
	return nil
}

// loadConfig applies the device override order: flag, VOICE_DEVICE, config.
func loadConfig(path, device string) (*preset.Config, error) {
	cfg, err := preset.Load(path, settingsPath(path))
	if err != nil {
		return nil, err
	}
	// settings.toml uses these words where an empty value could not
	// override config.toml.
	for _, p := range []*string{&cfg.Device, &cfg.Listen.InputDevice} {
		if *p == "default" {
			*p = ""
		}
	}
	for _, p := range []*string{&cfg.Serve.StopHotkey, &cfg.Serve.MuteHotkey, &cfg.Serve.DictateHotkey, &cfg.Serve.ReaderHotkey} {
		if *p == "none" {
			*p = ""
		}
	}
	if device == "" {
		device = os.Getenv("VOICE_DEVICE")
	}
	if device != "" {
		cfg.Device = device
	}
	// Data lives in the tool folder unless the config moves it; never in the
	// cwd. A relative data_root is relative to the tool folder, relative
	// *_dir settings to data_root.
	root := toolDir()
	if cfg.DataRoot != "" {
		root = cfg.DataRoot
		if !filepath.IsAbs(root) {
			root = filepath.Join(toolDir(), root)
		}
	}
	abs := func(p string) string {
		if p != "" && !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return p
	}
	engine.Dirs.Models = dirOr(abs(cfg.ModelsDir), filepath.Join(root, "models"))
	engine.Dirs.Runtime = dirOr(abs(cfg.RuntimeDir), filepath.Join(root, "runtime"))
	core.SetDataDir(dirOr(abs(cfg.StateDir), filepath.Join(root, "data")))
	clones.Dir = dirOr(abs(cfg.VoicesDir), filepath.Join(root, "voices"))
	// The sidecar's Python code ships next to voice.exe; its environment and
	// model weights go to the (movable) sidecar dir.
	httpengine.SidecarCode = filepath.Join(exeDir(), "sidecar")
	httpengine.SidecarDir = dirOr(abs(cfg.SidecarDir), filepath.Join(root, "sidecar"))
	core.RegisterClones(cfg)
	return cfg, nil
}

func dirOr(dir, def string) string {
	if dir == "" {
		return def
	}
	return dir
}

func runModels(args []string) error {
	fs, cfgPath := flags("models")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: voice models [list] | voice models pull <name|all>...")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 || rest[0] == "list" {
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		defer w.Flush()
		for _, it := range models.Catalog {
			state := "-"
			if models.Installed(it, engine.Dirs.Models, engine.Dirs.Runtime) {
				state = "installed"
			}
			fmt.Fprintf(w, "%s\t%s\t%d MB\t%s\n", it.Name, state, it.SizeMB, it.Description)
		}
		return nil
	}
	if rest[0] != "pull" || len(rest) < 2 {
		fs.Usage()
		return fmt.Errorf("unknown models command %q", strings.Join(rest, " "))
	}
	var items []models.Item
	for _, name := range rest[1:] {
		if name == "all" {
			items = append(items, models.Catalog...)
			continue
		}
		it, ok := models.Find(name)
		if !ok {
			return fmt.Errorf("unknown model %q; see `voice models`", name)
		}
		items = append(items, it)
	}
	for _, it := range items {
		last := -1
		err := models.Pull(context.Background(), it, engine.Dirs.Models, engine.Dirs.Runtime, func(done, total int64) {
			if total <= 0 {
				return
			}
			if pct := int(done * 100 / total); pct/10 != last/10 {
				last = pct
				fmt.Fprintf(os.Stderr, "\r%s: %3d%%", it.Name, pct)
			}
		})
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Printf("%s installed\n", it.Name)
	}
	return nil
}

func runDevices(args []string) error {
	fs, cfgPath := flags("devices")
	fs.Parse(args)
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return err
	}
	names, err := audio.Devices()
	if err != nil {
		return err
	}
	selected := -1
	if cfg.Device != "" {
		for i, n := range names {
			if strings.Contains(strings.ToLower(n), strings.ToLower(cfg.Device)) {
				selected = i
				break
			}
		}
	}
	for i, n := range names {
		mark := " "
		if i == selected {
			mark = "*"
		}
		fmt.Printf("%s %d  %s\n", mark, i, n)
	}
	if cfg.Device == "" {
		fmt.Println("(using the system default device)")
	} else if selected < 0 {
		fmt.Printf("(configured device %q not found)\n", cfg.Device)
	}
	inputs, err := audio.InputDevices()
	if err != nil {
		return err
	}
	fmt.Println("\nmicrophones ([listen] input_device):")
	for i, n := range inputs {
		mark := " "
		if want := cfg.Listen.InputDevice; want != "" && strings.Contains(strings.ToLower(n), strings.ToLower(want)) {
			mark = "*"
		}
		fmt.Printf("%s %d  %s\n", mark, i, n)
	}
	return nil
}

// runHook announces client events. It must never break the client: every
// outcome exits 0, and speech is queued in the background process so the
// hook returns at once.
func runHook(args []string) error {
	fs, cfgPath := flags("hook")
	verbose := fs.Bool("v", false, "explain why nothing is said")
	fs.Parse(args)
	quiet := func(reason string) error {
		if *verbose {
			fmt.Fprintln(os.Stderr, "voice hook:", reason)
		}
		return nil
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return quiet("usage: voice hook claude (stdin) | voice hook codex <json> | voice hook codex-hooks (stdin)")
	}
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return quiet(err.Error())
	}
	agent := hookAgent()
	if rest[0] == "codex-hooks" {
		// Codex's lifecycle hooks carry dictation only; its notify command announces.
		input, _ := io.ReadAll(os.Stdin)
		deliverDictation(*cfgPath, agent, input, "codex")
		return nil
	}
	var claudeInput []byte
	if rest[0] == "claude" {
		claudeInput, _ = io.ReadAll(os.Stdin)
		// Dictation reaches a busy agent through its hooks, announcements or not.
		if delivered := deliverDictation(*cfgPath, agent, claudeInput, "claude"); delivered {
			return quiet("passed dictation to the agent")
		}
	}
	h := cfg.Hooks
	if h.Enabled != nil && !*h.Enabled {
		return quiet("hooks disabled")
	}
	seconds := func(p *float64) time.Duration {
		if p == nil {
			return 0
		}
		return time.Duration(*p * float64(time.Second))
	}
	var (
		ev     hooks.Event
		ok     bool
		reason string
	)
	switch rest[0] {
	case "claude":
		ev, ok, reason = hooks.FromClaude(bytes.NewReader(claudeInput), seconds(h.MinTurnSeconds))
	case "codex":
		if len(rest) < 2 {
			return quiet("codex notify payload missing")
		}
		payload := rest[len(rest)-1]
		// Codex has a single notify command. `--chain cmd args...` keeps the
		// one voice replaced: it runs first with the same payload appended.
		if len(rest) > 3 && rest[1] == "--chain" {
			chain := exec.Command(rest[2], append(rest[3:len(rest)-1], payload)...)
			if err := chain.Start(); err != nil {
				fmt.Fprintln(os.Stderr, "voice hook: chained notify:", err)
			} else {
				chain.Process.Release()
			}
		}
		ev, ok, reason = hooks.FromCodex(payload)
		// A thread working toward a goal starts its next turn at once: wait a
		// moment (the setting) and stay quiet while it goes on. Other turns are
		// announced at once.
		if wait := cfg.Hooks.GoalWait(); ok && ev.Thread != "" && wait > 0 {
			if codex, _, err := clientHomes(); err == nil && hooks.CodexHasGoal(codex, ev.Thread) {
				time.Sleep(wait)
				if hooks.CodexContinues(codex, ev.Thread) {
					return quiet("Codex goes on with the thread's goal")
				}
			}
		}
		if ok {
			// The turn is over; what waits for it stays for its next hook or the queue.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			newClient(*cfgPath).Agent(ctx, service.AgentArgs{PID: agent, Client: "codex", Project: ev.Project, Event: "ended"})
			cancel()
		}
	default:
		return quiet("unknown client " + rest[0])
	}
	if !ok {
		return quiet(reason)
	}
	// With voice replies on, the mod reads the question itself.
	if ev.Tool == "AskUserQuestion" && cfg.Serve.VoiceReplies != nil && *cfg.Serve.VoiceReplies {
		return quiet("the mod reads the question aloud")
	}
	if core.SharedMuted() {
		return quiet("muted")
	}
	// The agent just said its answer aloud (voice replies): no announcement on top.
	// SinceAsked covers its speech still playing when the turn ends.
	if cfg.Serve.VoiceReplies != nil && *cfg.Serve.VoiceReplies && min(core.SinceLastSpoke(), core.SinceAsked(agent)) < 30*time.Second {
		return quiet("the agent answered aloud")
	}
	if skip := seconds(h.SkipAfterSpeech); min(core.SinceLastSpoke(), core.SinceAsked(agent)) < skip {
		return quiet("the agent spoke recently")
	}
	text, cacheable := hooks.Compose(h, ev, cfg.DefaultLang)
	if text == "" {
		return quiet("no phrase for " + ev.Kind)
	}
	presetName := h.Preset
	if presetName == "" {
		presetName = os.Getenv("VOICE_PRESET")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Queue and return: the background process speaks while the client goes on.
	_, err = newClient(*cfgPath).Speak(ctx, service.SpeakArgs{Text: text, Intent: ev.Intent, Preset: presetName, Cache: cacheable, Client: ev.Client, PID: agent, Project: ev.Project, Source: "hook"})
	if err != nil {
		return quiet(err.Error())
	}
	return quiet("saying: " + text)
}

// runMCP serves MCP on stdio as a thin front end: speech, listening and
// cloning run in the background process.
func runMCP(args []string) error {
	fs, cfgPath := flags("mcp")
	presetName := fs.String("preset", os.Getenv("VOICE_PRESET"), "voice preset for this client's speech")
	client := fs.String("client", "", "client name (claude, codex, ...) for [clients] presets and hooks")
	fs.Parse(args)
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	c := newClient(*cfgPath)
	// Start the background process now so the first sentence is not late;
	// distribution smoke tests only list tools and must leave nothing running.
	if os.Getenv("CODEX_TOOLS_SMOKE_TEST") == "" {
		go c.Ensure(context.Background())
	}
	return mcpserver.New(c, version, *client, *presetName).Run(context.Background(), &mcp.StdioTransport{})
}
func runPresets(args []string) error {
	fs, cfgPath := flags("presets")
	fs.Parse(args)
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	defer w.Flush()
	for _, name := range cfg.PresetNames() {
		r, err := cfg.Resolve(name)
		if err != nil {
			fmt.Fprintf(w, "%s\t!\t%v\n", name, err)
			continue
		}
		mark := ""
		if name == cfg.DefaultPreset {
			mark = " (default)"
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\n", name, mark, r.Engine, r.Description)
	}
	return nil
}

func runVoices(args []string) error {
	fs, cfgPath := flags("voices")
	only := fs.String("engine", "", "engine name (default: all)")
	fs.Parse(args)
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return err
	}
	names := []string{*only}
	if *only == "" {
		names = names[:0]
		for n := range cfg.Engines {
			names = append(names, n)
		}
		sort.Strings(names)
	}
	sp := core.NewSpeaker(cfg)
	defer sp.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	defer w.Flush()
	for _, n := range names {
		e, err := sp.Engine(n)
		if err != nil {
			fmt.Fprintf(w, "%s\t!\t%v\n", n, err)
			continue
		}
		voices, err := e.Voices(ctx)
		if err != nil {
			fmt.Fprintf(w, "%s\t!\t%v\n", n, err)
			continue
		}
		for _, v := range voices {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n, v.Name, v.Lang, v.Gender)
		}
	}
	return nil
}
