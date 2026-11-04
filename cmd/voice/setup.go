package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"voice/internal/autostart"
	"voice/internal/integrate"
)

// runSetup is what mcp-install runs through the tool.json hooks: before an
// update replaces binary-runtime it stops the running copy, it can set
// data_root in the staged config, and after registration it adds what voice
// needs in the clients beyond the MCP entry.
func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	stop := fs.String("stop", "", "stop voice processes running from this binary-runtime folder")
	config := fs.String("config", "", "config.toml to write -data-root into")
	dataRoot := fs.String("data-root", "", "where models, voices, cache and the sidecar go")
	clients := fs.Bool("clients", false, "add the Codex notify command and timeout, and the Claude Code hooks")
	removeClients := fs.Bool("remove-clients", false, "undo -clients (uninstall)")
	fs.Parse(args)
	if *stop != "" {
		stopInstalled(*stop)
	}
	if *dataRoot != "" {
		if *config == "" {
			return errors.New("-data-root needs -config")
		}
		if err := integrate.SetDataRoot(*config, *dataRoot); err != nil {
			return err
		}
		fmt.Printf("voice: data_root = %s\n", *dataRoot)
	}
	if *clients {
		return setupClients()
	}
	if *removeClients {
		return removeClientSetup()
	}
	return nil
}

func clientHomes() (codex, claude string, err error) {
	codex = os.Getenv("CODEX_HOME")
	if codex == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		codex = filepath.Join(home, ".codex")
	}
	// Like mcp-install: only an existing Claude Code configuration folder.
	claude = os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		home, _ := os.UserHomeDir()
		claude = filepath.Join(home, ".claude")
	}
	if st, err := os.Stat(claude); err != nil || !st.IsDir() {
		claude = ""
	}
	return codex, claude, nil
}

// runAutostart turns the login shortcut on or off.
func runAutostart(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	switch strings.Join(args, " ") {
	case "on", "off":
		if err := autostart.Set(args[0] == "on", exe); err != nil {
			return err
		}
	case "", "status":
	default:
		return errors.New("usage: voice autostart on|off|status")
	}
	fmt.Printf("autostart at login: %v\n", map[bool]string{true: "on", false: "off"}[autostart.Enabled()])
	return nil
}

// removeClientSetup takes voice out of Codex notify, the Claude Code hooks and login autostart.
func removeClientSetup() error {
	if autostart.Enabled() {
		if err := autostart.Set(false, ""); err != nil {
			return err
		}
		fmt.Println("voice: removed from login autostart")
	}
	codex, claude, err := clientHomes()
	if err != nil {
		return err
	}
	if changed, err := integrate.RemoveCodex(filepath.Join(codex, "config.toml")); err != nil {
		return err
	} else if changed {
		fmt.Println("voice: removed from Codex notify")
	}
	if changed, err := integrate.RemoveCodexHooks(filepath.Join(codex, "hooks.json")); err != nil {
		return err
	} else if changed {
		fmt.Println("voice: removed from Codex hooks")
	}
	if changed, err := integrate.RemoveCodexSkill(codex); err != nil {
		return err
	} else if changed {
		fmt.Println("voice: removed Codex's $character skill")
	}
	if claude == "" {
		return nil
	}
	if changed, err := integrate.RemoveClaude(filepath.Join(claude, "settings.json")); err != nil {
		return err
	} else if changed {
		fmt.Println("voice: removed from Claude Code hooks")
	}
	claudeMod(false)
	return nil
}

// stopInstalled asks the installed background process to quit (it restores
// ducked apps and the mixer), then ends whatever still runs from there:
// Windows does not replace a folder whose program is running.
func stopInstalled(runtimeDir string) {
	exe := filepath.Join(runtimeDir, "voice.exe")
	if _, err := os.Stat(exe); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "serve", "stop")
	hidden(cmd)
	_ = cmd.Run()
	if n := killUnder(runtimeDir); n > 0 {
		fmt.Printf("voice: stopped %d process(es) running from %s\n", n, runtimeDir)
	}
}

func setupClients() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	codex, claude, err := clientHomes()
	if err != nil {
		return err
	}
	server := "voice"
	if m, err := os.ReadFile(filepath.Join(toolDir(), "tool.json")); err == nil {
		var tool struct{ Name, Server string }
		if json.Unmarshal(m, &tool) == nil && tool.Server != "" {
			server = tool.Server
		}
	}
	codexConfig := filepath.Join(codex, "config.toml")
	if changed, err := integrate.Codex(codexConfig, server, exe); err != nil {
		return err
	} else if changed {
		fmt.Printf("voice: Codex notify and tool timeout set in %s\n", codexConfig)
	}
	if _, err := os.Stat(codexConfig); err == nil {
		codexHooks := filepath.Join(codex, "hooks.json")
		if changed, err := integrate.CodexHooks(codexHooks, exe); err != nil {
			return err
		} else if changed {
			fmt.Printf("voice: Codex hooks set in %s (trust them once with /hooks in Codex)\n", codexHooks)
		}
		if changed, err := integrate.CodexSkill(codex); err != nil {
			return err
		} else if changed {
			fmt.Println("voice: Codex gets the $character skill")
		}
	}
	if claude == "" {
		return nil
	}
	settings := filepath.Join(claude, "settings.json")
	if changed, err := integrate.Claude(settings, exe); err != nil {
		return err
	} else if changed {
		fmt.Printf("voice: Claude Code hooks set in %s\n", settings)
	}
	claudeMod(true)
	return nil
}

// claudeMod installs (or removes) the voice-dictation mod in Claude Code
// through its own CLI: the mod lets dictation start a turn in a session that
// is not on screen. Without the claude command, or the mod's folder in this
// install, there is nothing to do; a failure is reported, not fatal.
func claudeMod(install bool) {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return
	}
	dir := filepath.Join(toolDir(), "claude-plugin", "voice-dictation")
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); install && err != nil {
		return
	}
	run := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, claude, append([]string{"plugin"}, args...)...)
		hidden(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if !install {
		run("uninstall", "voice-dictation@voice", "-s", "user")
		if run("uninstall", "voice@voice", "-s", "user") == nil {
			fmt.Println("voice: removed the Claude Code dictation mod")
		}
		run("marketplace", "remove", "voice")
		return
	}
	// add fails when the marketplace is there already: then update reads it again.
	if run("marketplace", "add", dir) != nil {
		run("marketplace", "update", "voice")
	}
	run("uninstall", "voice-dictation@voice", "-s", "user") // its name before 0.2.0
	if err := run("install", "voice@voice", "-s", "user"); err != nil {
		fmt.Printf("voice: the Claude Code dictation mod was not installed: %v\n", err)
		return
	}
	run("update", "voice@voice", "-s", "user")
	fmt.Println("voice: Claude Code dictation mod installed (new sessions load it)")
}
