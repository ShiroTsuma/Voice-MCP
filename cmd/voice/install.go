package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"voice/internal/integrate"
)

// runInstall sets voice up from the folder it was unpacked to (a release
// archive, no installer): config.toml from the example, the MCP server and
// the voice skill in Codex and Claude Code, then what `setup -clients` adds
// (hooks, notify, the $character skill and the Claude Code mod).
func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	dataRoot := fs.String("data-root", "", "keep models, voices, cache and the sidecar in this folder")
	fs.Parse(args)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := toolDir()
	config := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(config); errors.Is(err, os.ErrNotExist) {
		if example, err := os.ReadFile(filepath.Join(dir, "config.example.toml")); err == nil {
			if err := os.WriteFile(config, example, 0o644); err != nil {
				return err
			}
			fmt.Printf("voice: %s created from the example\n", config)
		}
	}
	if *dataRoot != "" {
		if err := integrate.SetDataRoot(config, *dataRoot); err != nil {
			return err
		}
		fmt.Printf("voice: data_root = %s\n", *dataRoot)
	}
	codex, claude, err := clientHomes()
	if err != nil {
		return err
	}
	skill, _ := os.ReadFile(filepath.Join(dir, "skills", "voice", "SKILL.md"))
	codexConfig := filepath.Join(codex, "config.toml")
	if changed, err := integrate.CodexServer(codexConfig, "voice", exe); err != nil {
		return err
	} else if changed {
		fmt.Printf("voice: MCP server registered in %s\n", codexConfig)
	}
	if _, err := os.Stat(codexConfig); err == nil && len(skill) > 0 {
		if changed, err := integrate.Skill(codex, skill); err != nil {
			return err
		} else if changed {
			fmt.Println("voice: Codex gets the voice skill")
		}
	}
	if claude != "" {
		if len(skill) > 0 {
			if changed, err := integrate.Skill(claude, skill); err != nil {
				return err
			} else if changed {
				fmt.Println("voice: Claude Code gets the voice skill")
			}
		}
		claudeServer(exe, true)
	}
	if err := setupClients(); err != nil {
		return err
	}
	fmt.Println(`
voice is installed. Next:
  voice models pull runtime kokoro vad asr-parakeet   (the voices and the recognizer)
  voice say "Hello"                                    (check that you hear it)
Then restart Codex and Claude Code; in Codex run /hooks once and trust voice's hooks.`)
	return nil
}

// runUninstall undoes runInstall; the folder itself, with its models and
// settings, stays for the user to delete.
func runUninstall(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	stopInstalled(filepath.Dir(exe))
	if err := removeClientSetup(); err != nil {
		return err
	}
	codex, claude, err := clientHomes()
	if err != nil {
		return err
	}
	skill, _ := os.ReadFile(filepath.Join(toolDir(), "skills", "voice", "SKILL.md"))
	if changed, err := integrate.RemoveCodexServer(filepath.Join(codex, "config.toml"), "voice"); err != nil {
		return err
	} else if changed {
		fmt.Println("voice: removed from Codex's MCP servers")
	}
	if len(skill) > 0 {
		integrate.RemoveSkill(codex, skill)
	}
	if claude != "" {
		if len(skill) > 0 {
			integrate.RemoveSkill(claude, skill)
		}
		claudeServer(exe, false)
	}
	fmt.Println("voice: uninstalled; delete this folder to remove its models and settings too")
	return nil
}

// claudeServer registers (or removes) the MCP server in Claude Code through
// its CLI, at user scope. Without the claude command it says how to do it.
func claudeServer(exe string, add bool) {
	claude, err := exec.LookPath("claude")
	if err != nil {
		if add {
			fmt.Printf("voice: no claude command on PATH; register voice in Claude Code with:\n  claude mcp add voice --scope user -- \"%s\" mcp\n", exe)
		}
		return
	}
	run := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, claude, append([]string{"mcp"}, args...)...)
		hidden(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	run("remove", "voice", "-s", "user")
	if !add {
		fmt.Println("voice: removed from Claude Code's MCP servers")
		return
	}
	if err := run("add", "voice", "--scope", "user", "--", exe, "mcp"); err != nil {
		fmt.Printf("voice: registering in Claude Code failed: %v\n", err)
		return
	}
	fmt.Println("voice: MCP server registered in Claude Code")
}
