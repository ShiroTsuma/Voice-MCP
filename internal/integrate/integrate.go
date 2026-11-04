// Package integrate wires voice into the clients beyond what mcp-install
// does for every tool: Codex gets a longer tool timeout and voice as its
// notify command, Claude Code gets Notification/Stop hooks, and the data
// root can be set in config.toml. mcp-install runs these through the
// tool.json hooks (`voice setup`).
package integrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var (
	tomlHeader  = regexp.MustCompile(`^\s*\[{1,2}\s*([^\]]+?)\s*\]{1,2}\s*(#.*)?$`)
	timeoutKey  = regexp.MustCompile(`^\s*tool_timeout_sec\s*=`)
	notifyKey   = regexp.MustCompile(`^\s*notify\s*=`)
	dataRootKey = regexp.MustCompile(`^\s*data_root\s*=`)
	voiceHook   = regexp.MustCompile(`voice(\.exe)?"?\s+hook\s+(claude|codex-hooks)\b`)
)

// ListenTimeout is the Codex tool timeout voice_listen needs: it waits for
// the user to answer.
const ListenTimeout = "120.0"

func splitLines(s string) ([]string, string) {
	nl := "\n"
	if strings.Contains(s, "\r\n") {
		nl = "\r\n"
	}
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil, nl
	}
	// Split on LF and drop each CR, so a file with mixed endings still
	// reads line by line; it is written back with nl.
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines, nl
}

func header(line string) (string, bool) {
	m := tomlHeader.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// tomlString writes a literal string when possible.
func tomlString(v string) string {
	if !strings.ContainsAny(v, "'\r\n\t") {
		return "'" + v + "'"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func tomlArray(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = tomlString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// writeIfChanged replaces path atomically, only when the content differs.
func writeIfChanged(path string, old, content []byte) (bool, error) {
	if bytes.Equal(old, content) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".voice-tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

func readOptional(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")), err
}

// SetDataRoot writes data_root at the top of config.toml, replacing an
// existing top-level value.
func SetDataRoot(configPath, root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	old, err := readOptional(configPath)
	if err != nil {
		return err
	}
	lines, nl := splitLines(string(old))
	line := "data_root = " + tomlString(abs)
	at := -1
	for i, l := range lines {
		if _, ok := header(l); ok {
			break
		}
		if dataRootKey.MatchString(l) {
			at = i
			break
		}
	}
	if at >= 0 {
		lines[at] = line
	} else {
		lines = append([]string{line}, lines...)
	}
	_, err = writeIfChanged(configPath, old, []byte(strings.Join(lines, nl)+nl))
	return err
}

// Codex sets tool_timeout_sec for the voice server, unless the user set one,
// and makes voice the notify command. Codex runs a single notify command,
// so an existing one (the Codex app's own) is chained after voice.
func Codex(configPath, server, exe string) (bool, error) {
	old, err := readOptional(configPath)
	if err != nil || old == nil {
		return false, err
	}
	lines, nl := splitLines(string(old))

	section, serverAt, hasTimeout := "", -1, false
	for i, l := range lines {
		if s, ok := header(l); ok {
			section = s
			if strings.EqualFold(s, "mcp_servers."+server) {
				serverAt = i
			}
			continue
		}
		if strings.EqualFold(section, "mcp_servers."+server) && timeoutKey.MatchString(l) {
			hasTimeout = true
		}
	}
	if serverAt >= 0 && !hasTimeout {
		// After the section's keys, before its subtables.
		at := serverAt + 1
		for at < len(lines) {
			if _, ok := header(lines[at]); ok {
				break
			}
			at++
		}
		for at > serverAt+1 && strings.TrimSpace(lines[at-1]) == "" {
			at--
		}
		lines = append(lines[:at], append([]string{"tool_timeout_sec = " + ListenTimeout}, lines[at:]...)...)
	}

	notify := []string{exe, "hook", "codex"}
	firstTable, at := len(lines), -1
	for i, l := range lines {
		if _, ok := header(l); ok {
			firstTable = i
			break
		}
		if notifyKey.MatchString(l) {
			at = i
		}
	}
	if at >= 0 {
		var v struct{ Notify []string }
		if _, err := toml.Decode(lines[at], &v); err != nil {
			return false, fmt.Errorf("%s: notify: %w", configPath, err)
		}
		switch old := v.Notify; {
		case isVoiceNotify(old):
			// Re-registration: keep what an earlier install chained.
			if len(old) > 4 && old[3] == "--chain" {
				notify = append(append(notify, "--chain"), unnest(old[4:])...)
			}
		case len(old) > 0:
			notify = append(append(notify, "--chain"), unnest(old)...)
		}
	}
	value := "notify = " + tomlArray(notify)
	if at >= 0 {
		lines[at] = value
	} else {
		// Top-level keys must come before the first table.
		lines = append(lines[:firstTable], append([]string{value}, lines[firstTable:]...)...)
	}
	return writeIfChanged(configPath, old, []byte(strings.Join(lines, nl)+nl))
}

// unnest drops voice from inside a chained command. The Codex app's
// computer-use helper wraps the notify it finds as `--previous-notify <json>`;
// after voice chained it, an app update wraps voice's notify that way, and
// voice would run twice (and the helper's old version with it). The wrapped
// voice gives way to what it chained, or goes when that is the same helper.
func unnest(cmd []string) []string {
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] != "--previous-notify" {
			continue
		}
		var prev []string
		if json.Unmarshal([]byte(cmd[i+1]), &prev) != nil || !isVoiceNotify(prev) {
			return cmd
		}
		var inner []string
		if len(prev) > 4 && prev[3] == "--chain" {
			inner = unnest(prev[4:])
		}
		out := append([]string{}, cmd[:i]...)
		if len(inner) > 0 && !strings.EqualFold(filepath.Base(inner[0]), filepath.Base(cmd[0])) {
			out = append(out, "--previous-notify", string(mustMarshal(inner)))
		}
		return append(out, cmd[i+2:]...)
	}
	return cmd
}

// RemoveCodex undoes Codex: a voice notify command gives way to the command it chained,
// or goes away. The server section itself belongs to mcp-install.
func RemoveCodex(configPath string) (bool, error) {
	old, err := readOptional(configPath)
	if err != nil || old == nil {
		return false, err
	}
	lines, nl := splitLines(string(old))
	for i, l := range lines {
		if _, ok := header(l); ok {
			break
		}
		if !notifyKey.MatchString(l) {
			continue
		}
		var v struct{ Notify []string }
		if _, err := toml.Decode(l, &v); err != nil || !isVoiceNotify(v.Notify) {
			return false, err
		}
		if len(v.Notify) > 4 && v.Notify[3] == "--chain" {
			lines[i] = "notify = " + tomlArray(v.Notify[4:])
		} else {
			lines = append(lines[:i], lines[i+1:]...)
		}
		return writeIfChanged(configPath, old, []byte(strings.Join(lines, nl)+nl))
	}
	return false, nil
}

// RemoveClaude drops voice's entries from the Claude Code hooks, and groups or events left empty.
func RemoveClaude(settingsPath string) (bool, error) {
	old, err := readOptional(settingsPath)
	if err != nil || len(bytes.TrimSpace(old)) == 0 {
		return false, err
	}
	doc, err := parseObject(old)
	if err != nil {
		return false, fmt.Errorf("%s: %w", settingsPath, err)
	}
	raw, ok := doc.vals["hooks"]
	if !ok {
		return false, nil
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return false, fmt.Errorf("%s: hooks: %w", settingsPath, err)
	}
	keptKeys := hooks.keys[:0:0]
	for _, event := range hooks.keys {
		var groups []json.RawMessage
		if json.Unmarshal(hooks.vals[event], &groups) != nil {
			keptKeys = append(keptKeys, event)
			continue
		}
		groups = withoutVoice(groups)
		if len(groups) == 0 {
			delete(hooks.vals, event)
			continue
		}
		hooks.vals[event] = mustMarshal(groups)
		keptKeys = append(keptKeys, event)
	}
	hooks.keys = keptKeys
	if len(hooks.keys) == 0 {
		delete(doc.vals, "hooks")
		for i, k := range doc.keys {
			if k == "hooks" {
				doc.keys = append(doc.keys[:i], doc.keys[i+1:]...)
				break
			}
		}
	} else {
		doc.vals["hooks"] = mustMarshal(hooks)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, mustMarshal(doc), "", "  "); err != nil {
		return false, err
	}
	out.WriteByte('\n')
	return writeIfChanged(settingsPath, old, out.Bytes())
}

// withoutVoice removes `voice hook claude` commands from hook groups and drops emptied groups.
func withoutVoice(groups []json.RawMessage) []json.RawMessage {
	var kept []json.RawMessage
	for _, g := range groups {
		group, err := parseObject(g)
		if err != nil {
			kept = append(kept, g)
			continue
		}
		var list []json.RawMessage
		_ = json.Unmarshal(group.vals["hooks"], &list)
		var others []json.RawMessage
		for _, h := range list {
			var cmd struct{ Command string }
			if json.Unmarshal(h, &cmd) == nil && voiceHook.MatchString(cmd.Command) {
				continue
			}
			others = append(others, h)
		}
		if len(others) == 0 {
			continue
		}
		if len(others) != len(list) {
			group.set("hooks", mustMarshal(others))
			g = mustMarshal(group)
		}
		kept = append(kept, g)
	}
	return kept
}

func isVoiceNotify(cmd []string) bool {
	if len(cmd) < 3 || cmd[1] != "hook" || cmd[2] != "codex" {
		return false
	}
	base := strings.ToLower(filepath.Base(cmd[0]))
	return base == "voice.exe" || base == "voice"
}

// Claude adds `voice hook claude` to the Notification, Stop, UserPromptSubmit
// and PostToolUse hooks of
// Claude Code's settings.json, replacing earlier voice entries and keeping
// everything else in its order.
func Claude(settingsPath, exe string) (bool, error) {
	// SessionStart registers a new session for dictation, UserPromptSubmit and
	// PostToolUse tell voice a turn runs and pass on what the user dictates
	// meanwhile; Notification and Stop also announce.
	return setHooks(settingsPath, `"`+exe+`" hook claude`, 0,
		"Notification", "Stop", "SessionStart", "UserPromptSubmit", "PostToolUse")
}

// CodexHooks adds `voice hook codex-hooks` to Codex's hooks.json, for
// dictation only (Codex's notify command announces): a new session registers,
// a running turn takes what the user dictates, Stop continues the turn with
// it, Interrupt ends the turn. Codex runs a hook only once the user trusted
// it (`/hooks` in Codex).
func CodexHooks(hooksPath, exe string) (bool, error) {
	command := shellWord(exe) + " hook codex-hooks"
	changed, err := setHooks(hooksPath, command, 10, "SessionStart", "UserPromptSubmit", "PostToolUse", "Stop")
	if err != nil {
		return changed, err
	}
	// Codex allows an Interrupt hook 3 seconds at most.
	more, err := setHooks(hooksPath, command, 3, "Interrupt")
	return changed || more, err
}

// RemoveCodexHooks drops voice's entries from Codex's hooks.json.
func RemoveCodexHooks(hooksPath string) (bool, error) { return RemoveClaude(hooksPath) }

// shellWord quotes a path only when it has to: Codex runs a hook through the
// user's shell, and PowerShell reads a quoted first word as a string, not a
// command.
func shellWord(p string) string {
	if strings.ContainsAny(p, " \t'") {
		return `"` + p + `"`
	}
	return p
}

// setHooks puts command on events in a Claude Code settings file or a Codex
// hooks.json (the same shape), replacing voice's earlier entries and keeping
// everyone else's.
func setHooks(settingsPath, command string, timeout int, events ...string) (bool, error) {
	old, err := readOptional(settingsPath)
	if err != nil {
		return false, err
	}
	doc := &object{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(old)) > 0 {
		if doc, err = parseObject(old); err != nil {
			return false, fmt.Errorf("%s: %w", settingsPath, err)
		}
	}
	hooks := &object{vals: map[string]json.RawMessage{}}
	if raw, ok := doc.vals["hooks"]; ok {
		if hooks, err = parseObject(raw); err != nil {
			return false, fmt.Errorf("%s: hooks: %w", settingsPath, err)
		}
	}
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout,omitempty"`
	}
	entry := mustMarshal(map[string]any{"hooks": []hook{{"command", command, timeout}}})
	for _, event := range events {
		var groups []json.RawMessage
		if raw, ok := hooks.vals[event]; ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return false, fmt.Errorf("%s: hooks.%s: %w", settingsPath, event, err)
			}
		}
		hooks.set(event, mustMarshal(append(withoutVoice(groups), entry)))
	}
	doc.set("hooks", mustMarshal(hooks))
	var out bytes.Buffer
	if err := json.Indent(&out, mustMarshal(doc), "", "  "); err != nil {
		return false, err
	}
	out.WriteByte('\n')
	return writeIfChanged(settingsPath, old, out.Bytes())
}

// mustMarshal keeps <, > and & as they are: hook commands use them.
func mustMarshal(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

// object is a JSON object that keeps its key order, so rewriting a user's
// settings file does not shuffle it.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(key, v)
	}
	return o, nil
}

func (o *object) set(key string, v json.RawMessage) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// codexCharacterSkill is Codex's $character: Codex runs no plugin code, so the
// agent switches through voice's MCP tool.
const codexCharacterSkill = `---
name: character
description: Switch the voice character this Codex session plays (its name, personality and voice), or list the characters. Use when the user types $character, optionally with a name, or asks to change their voice character.
metadata:
  short-description: Pick this session's voice character
---

# Voice character

The user called this with a character's name after it, or with nothing.

- With no name: call the voice MCP tool voice_control with action "list_characters", show the names with their short descriptions, and ask which one they want. Wait for the answer.
- With a name: call voice_control with action "set_character" and character set to that name.

Then reply in one short sentence, already in the new character. If voice says the name is unknown, show the list instead.
`

// codexSkill is where CodexSkill writes $character.
func codexSkill(codexHome string) string {
	return filepath.Join(codexHome, "skills", "character", "SKILL.md")
}

// CodexSkill writes the $character skill into Codex's skills folder, and
// removes the /prompts:character file an earlier voice wrote (Codex dropped
// custom prompts).
func CodexSkill(codexHome string) (bool, error) {
	if old, _ := readOptional(filepath.Join(codexHome, "prompts", "character.md")); bytes.Contains(old, []byte("Voice character: \"$ARGUMENTS\"")) {
		os.Remove(filepath.Join(codexHome, "prompts", "character.md"))
	}
	path := codexSkill(codexHome)
	old, err := readOptional(path)
	if err != nil {
		return false, err
	}
	return writeIfChanged(path, old, []byte(codexCharacterSkill))
}

// RemoveCodexSkill deletes $character when it is still voice's.
func RemoveCodexSkill(codexHome string) (bool, error) {
	path := codexSkill(codexHome)
	old, err := readOptional(path)
	if err != nil || string(old) != codexCharacterSkill {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	os.Remove(filepath.Dir(path)) // the folder, when it is empty now
	return true, nil
}

// CodexServer registers voice as an MCP server in Codex's config.toml, for
// a copy installed without mcp-install: [mcp_servers.<server>] runs exe with
// "mcp". An existing section keeps its keys; only its command and args are
// set. A missing config.toml (no Codex) is left alone.
func CodexServer(configPath, server, exe string) (bool, error) {
	old, err := readOptional(configPath)
	if err != nil || old == nil {
		return false, err
	}
	lines, nl := splitLines(string(old))
	name := "mcp_servers." + server
	start, end := -1, len(lines)
	for i, l := range lines {
		if s, ok := header(l); ok {
			if start >= 0 {
				end = i
				break
			}
			if strings.EqualFold(s, name) {
				start = i
			}
		}
	}
	command := "command = " + tomlString(exe)
	args := `args = ["mcp"]`
	if start < 0 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "", "["+name+"]", command, args, "tool_timeout_sec = "+ListenTimeout)
	} else {
		commandKey := regexp.MustCompile(`^\s*command\s*=`)
		argsKey := regexp.MustCompile(`^\s*args\s*=`)
		hasCommand, hasArgs := false, false
		for i := start + 1; i < end; i++ {
			switch {
			case commandKey.MatchString(lines[i]):
				lines[i], hasCommand = command, true
			case argsKey.MatchString(lines[i]):
				lines[i], hasArgs = args, true
			}
		}
		add := []string{}
		if !hasCommand {
			add = append(add, command)
		}
		if !hasArgs {
			add = append(add, args)
		}
		lines = append(lines[:start+1], append(add, lines[start+1:]...)...)
	}
	return writeIfChanged(configPath, old, []byte(strings.Join(lines, nl)+nl))
}

// RemoveCodexServer deletes the [mcp_servers.<server>] section (and its
// subtables) when it runs voice.
func RemoveCodexServer(configPath, server string) (bool, error) {
	old, err := readOptional(configPath)
	if err != nil || old == nil {
		return false, err
	}
	lines, nl := splitLines(string(old))
	name := strings.ToLower("mcp_servers." + server)
	var out []string
	skip, removed := false, false
	for _, l := range lines {
		if s, ok := header(l); ok {
			s = strings.ToLower(s)
			skip = s == name || strings.HasPrefix(s, name+".")
			removed = removed || skip
		}
		if !skip {
			out = append(out, l)
		}
	}
	if !removed {
		return false, nil
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return writeIfChanged(configPath, old, []byte(strings.Join(out, nl)+nl))
}

// Skill copies voice's agent skill into a client's skills folder
// (<home>/skills/voice/SKILL.md).
func Skill(home string, content []byte) (bool, error) {
	path := filepath.Join(home, "skills", "voice", "SKILL.md")
	old, err := readOptional(path)
	if err != nil {
		return false, err
	}
	return writeIfChanged(path, old, content)
}

// RemoveSkill deletes the skill Skill wrote, when it is still the same.
func RemoveSkill(home string, content []byte) (bool, error) {
	path := filepath.Join(home, "skills", "voice", "SKILL.md")
	old, err := readOptional(path)
	if err != nil || old == nil || !bytes.Equal(old, content) {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	os.Remove(filepath.Dir(path))
	return true, nil
}

// HasHooks reports whether a Claude Code settings.json or Codex hooks.json
// runs voice's hooks. The file is JSON, so the command's quotes are escaped.
func HasHooks(path string) bool {
	b, err := readOptional(path)
	return err == nil && voiceHookInJSON.Match(b)
}

var voiceHookInJSON = regexp.MustCompile(`voice(\.exe)?(\\")?\s+hook\s+(claude|codex-hooks)\b`)

// HasCodexNotify reports whether Codex's config.toml runs voice as its
// notify command.
func HasCodexNotify(configPath string) bool {
	b, err := readOptional(configPath)
	if err != nil || b == nil {
		return false
	}
	lines, _ := splitLines(string(b))
	for _, l := range lines {
		if _, ok := header(l); ok {
			return false // notify is a top-level key
		}
		if notifyKey.MatchString(l) {
			var v struct{ Notify []string }
			if _, err := toml.Decode(l, &v); err == nil && isVoiceNotify(v.Notify) {
				return true
			}
		}
	}
	return false
}

// HasCodexSkill reports whether the $character skill is voice's.
func HasCodexSkill(codexHome string) bool {
	b, err := readOptional(codexSkill(codexHome))
	return err == nil && string(b) == codexCharacterSkill
}
