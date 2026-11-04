package integrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exe = `C:\tools\voice\binary-runtime\voice.exe`

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCodexChainsNotifyAndAddsTimeout(t *testing.T) {
	p := write(t, "config.toml", `model = "x"
notify = ["C:\\app\\computer-use.exe", "turn-ended"]

[mcp_servers.voice]
command = 'gw.exe'
args = ['--', 'voice.exe', 'mcp']

[mcp_servers.voice.tools.voice_speak]
approval_mode = "approve"
`)
	if _, err := Codex(p, "voice", exe); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	want := `notify = ['` + exe + `', 'hook', 'codex', '--chain', 'C:\app\computer-use.exe', 'turn-ended']`
	if !strings.Contains(got, want) {
		t.Fatalf("notify not chained:\n%s", got)
	}
	if !strings.Contains(got, "args = ['--', 'voice.exe', 'mcp']\ntool_timeout_sec = 120.0\n\n[mcp_servers.voice.tools") {
		t.Fatalf("timeout not in the server section:\n%s", got)
	}

	// A second run changes nothing: the chain is kept, the timeout not repeated.
	changed, err := Codex(p, "voice", exe)
	if err != nil || changed {
		t.Fatalf("second run changed=%v err=%v:\n%s", changed, err, read(t, p))
	}
}

func TestCodexUnnestsVoiceFromTheHelpersPreviousNotify(t *testing.T) {
	// The Codex app updated its computer-use helper and wrapped voice's notify
	// (which chained the helper's old version); voice then chained the new one.
	p := write(t, "config.toml", `notify = ['`+exe+`', 'hook', 'codex', '--chain', 'C:\app\new\codex-computer-use.exe', 'turn-ended', '--previous-notify', '["`+strings.ReplaceAll(exe, `\`, `\\`)+`","hook","codex","--chain","C:\\app\\old\\codex-computer-use.exe","turn-ended"]']
`)
	if _, err := Codex(p, "voice", exe); err != nil {
		t.Fatal(err)
	}
	want := `notify = ['` + exe + `', 'hook', 'codex', '--chain', 'C:\app\new\codex-computer-use.exe', 'turn-ended']`
	if got := read(t, p); !strings.Contains(got, want) {
		t.Fatalf("voice still runs twice:\n%s", got)
	}
	// Another program the helper wrapped stays chained behind it.
	got := unnest([]string{"helper.exe", "turn-ended", "--previous-notify", `["` + strings.ReplaceAll(exe, `\`, `\\`) + `","hook","codex","--chain","notify-send.exe","done"]`})
	if strings.Join(got, " ") != `helper.exe turn-ended --previous-notify ["notify-send.exe","done"]` {
		t.Fatalf("unnest: %q", got)
	}
}

func TestCodexKeepsUserTimeoutAndAddsNotify(t *testing.T) {
	p := write(t, "config.toml", "[mcp_servers.voice]\ncommand = 'gw'\ntool_timeout_sec = 300\n")
	if _, err := Codex(p, "voice", exe); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if strings.Count(got, "tool_timeout_sec") != 1 || !strings.HasPrefix(got, "notify = ['"+exe+"', 'hook', 'codex']\n[mcp_servers.voice]") {
		t.Fatalf("unexpected config:\n%s", got)
	}
}

func TestClaudeReplacesVoiceHooksKeepsOthers(t *testing.T) {
	p := write(t, "settings.json", `{
  "model": "opus",
  "hooks": {
    "Stop": [
      { "matcher": "", "hooks": [
        { "type": "command", "command": "\"D:\\old\\voice.exe\" hook claude" },
        { "type": "command", "command": "notify-send done && echo >log" }
      ] }
    ],
    "PreToolUse": [ { "hooks": [ { "type": "command", "command": "guard" } ] } ]
  },
  "theme": "dark"
}`)
	if _, err := Claude(p, exe); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	for _, want := range []string{`"notify-send done && echo >log"`, `"guard"`, `"matcher": ""`} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %s:\n%s", want, got)
		}
	}
	// One each for Notification, Stop, SessionStart, UserPromptSubmit and PostToolUse.
	if strings.Contains(got, `D:\\old`) || strings.Count(got, "hook claude") != 5 || !strings.Contains(got, `"PostToolUse"`) {
		t.Fatalf("voice hooks not replaced:\n%s", got)
	}
	if !(strings.Index(got, `"model"`) < strings.Index(got, `"hooks"`) && strings.Index(got, `"hooks"`) < strings.Index(got, `"theme"`)) {
		t.Fatalf("key order changed:\n%s", got)
	}
	if changed, err := Claude(p, exe); err != nil || changed {
		t.Fatalf("second run changed=%v err=%v", changed, err)
	}
}

func TestClaudeCreatesSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Claude(p, exe); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); !strings.Contains(got, `"Notification"`) || !strings.Contains(got, `hook claude`) {
		t.Fatalf("unexpected settings:\n%s", got)
	}
}

func TestRemoveRestoresChainedNotifyAndDropsHooks(t *testing.T) {
	cfg := write(t, "config.toml", "model = \"x\"\nnotify = ['"+exe+"', 'hook', 'codex', '--chain', 'C:\\app\\computer-use.exe', 'turn-ended']\n\n[mcp_servers.voice]\ncommand = 'gw'\n")
	if _, err := RemoveCodex(cfg); err != nil {
		t.Fatal(err)
	}
	if got := read(t, cfg); !strings.Contains(got, `notify = ['C:\app\computer-use.exe', 'turn-ended']`) || strings.Contains(got, "voice.exe") {
		t.Fatalf("chained notify not restored:\n%s", got)
	}
	alone := write(t, "alone.toml", "notify = ['"+exe+"', 'hook', 'codex']\nmodel = \"x\"\n")
	if _, err := RemoveCodex(alone); err != nil || read(t, alone) != "model = \"x\"\n" {
		t.Fatalf("lone notify not removed: %v %q", err, read(t, alone))
	}

	settings := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(settings, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo a && b"}]}]}}`), 0o644)
	if _, err := Claude(settings, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveClaude(settings); err != nil {
		t.Fatal(err)
	}
	got := read(t, settings)
	if strings.Contains(got, "hook claude") || strings.Contains(got, "Notification") || !strings.Contains(got, `"echo a && b"`) {
		t.Fatalf("voice hooks not removed cleanly:\n%s", got)
	}
	only := filepath.Join(t.TempDir(), "only.json")
	if _, err := Claude(only, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveClaude(only); err != nil || strings.Contains(read(t, only), "hooks") {
		t.Fatalf("empty hooks object left: %v %s", err, read(t, only))
	}
}

func TestSetDataRoot(t *testing.T) {
	p := write(t, "config.toml", "# comment\ndata_root = 'C:\\old'\n\n[serve]\ndata_root = 'not top level'\n")
	root := filepath.Join(t.TempDir(), "data")
	if err := SetDataRoot(p, root); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if !strings.Contains(got, "data_root = '"+root+"'\n\n[serve]\ndata_root = 'not top level'") {
		t.Fatalf("unexpected config:\n%s", got)
	}
	missing := filepath.Join(t.TempDir(), "new.toml")
	if err := SetDataRoot(missing, root); err != nil || read(t, missing) != "data_root = '"+root+"'\n" {
		t.Fatalf("new config: %v %q", err, read(t, missing))
	}
}

func TestCodexHooksKeepOthersAndRemoveCleanly(t *testing.T) {
	p := write(t, "hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"py check.py"}]}]}}`)
	if _, err := CodexHooks(p, exe); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "Interrupt"} {
		if !strings.Contains(got, `"`+ev+`"`) {
			t.Fatalf("no %s:\n%s", ev, got)
		}
	}
	// Unquoted: Codex may run it through PowerShell.
	if !strings.Contains(got, `"command": "C:\\tools\\voice\\binary-runtime\\voice.exe hook codex-hooks"`) || !strings.Contains(got, "py check.py") {
		t.Fatalf("unexpected hooks:\n%s", got)
	}
	if changed, _ := CodexHooks(p, exe); changed {
		t.Fatal("second run changed the file")
	}
	if _, err := RemoveCodexHooks(p); err != nil || strings.Contains(read(t, p), "codex-hooks") || !strings.Contains(read(t, p), "py check.py") {
		t.Fatalf("remove: %v\n%s", err, read(t, p))
	}
}

func TestCodexSkillWrittenAndRemovedOnlyWhenOurs(t *testing.T) {
	home := t.TempDir()
	if changed, err := CodexSkill(home); err != nil || !changed {
		t.Fatalf("write: %v %v", changed, err)
	}
	if changed, _ := CodexSkill(home); changed {
		t.Fatal("second write changed it")
	}
	if removed, err := RemoveCodexSkill(home); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	mine := filepath.Join(home, "skills", "character", "SKILL.md")
	os.MkdirAll(filepath.Dir(mine), 0o755)
	if err := os.WriteFile(mine, []byte("the user's own"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, _ := RemoveCodexSkill(home); removed {
		t.Fatal("removed the user's own prompt")
	}
}

func TestCodexServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("model = \"x\"\n\n[mcp_servers.other]\ncommand = 'o'\n"), 0o644)
	if changed, err := CodexServer(path, "voice", exe); err != nil || !changed {
		t.Fatalf("add: %v %v", changed, err)
	}
	b, _ := os.ReadFile(path)
	want := "[mcp_servers.voice]\ncommand = 'C:\\tools\\voice\\binary-runtime\\voice.exe'\nargs = [\"mcp\"]\ntool_timeout_sec = 120.0\n"
	if !strings.Contains(string(b), want) || !strings.Contains(string(b), "[mcp_servers.other]") {
		t.Fatalf("got:\n%s", b)
	}
	if changed, _ := CodexServer(path, "voice", exe); changed {
		t.Fatal("second run changed the file")
	}
	if changed, err := RemoveCodexServer(path, "voice"); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "voice") || !strings.Contains(string(b), "[mcp_servers.other]") {
		t.Fatalf("after remove:\n%s", b)
	}
}
