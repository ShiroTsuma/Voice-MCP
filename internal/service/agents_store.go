package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"voice/internal/preset"
)

// savedAgent is one session in the agents file: enough to recognise the same
// process after voice restarts, and what the user set for it.
type savedAgent struct {
	PID      uint32    `json:"pid"`
	Start    int64     `json:"start"` // process start time; a reused PID has another
	Exe      string    `json:"exe"`
	Name     string    `json:"name"`
	Client   string    `json:"client,omitempty"`
	Project  string    `json:"project,omitempty"`
	NoInput  bool      `json:"no_input,omitempty"`
	NoSpeech bool      `json:"no_speech,omitempty"`
	Custom   bool      `json:"custom,omitempty"`  // the name is the user's own
	Session  string    `json:"session,omitempty"` // Claude Code's or Codex's session id: the mod and codex queue need it
	At       time.Time `json:"at"`
}

// staleAfter drops a saved session nobody heard from for this long.
const staleAfter = 24 * time.Hour

// Load reads the sessions saved in path and keeps those whose process still
// runs: same PID, same start time and executable (Identify), active within a
// day. Later changes are saved there.
func (a *Agents) Load(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.path = path
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []savedAgent
	if json.Unmarshal(b, &list) != nil {
		return
	}
	if a.seen == nil {
		a.seen = map[uint32]*agentSeen{}
	}
	for _, e := range list {
		if time.Since(e.At) > staleAfter || a.Identify == nil {
			continue
		}
		start, exe, ok := a.Identify(e.PID)
		if !ok || start != e.Start || !strings.EqualFold(exe, e.Exe) {
			continue // ended while voice was off, or the PID belongs to another program now
		}
		a.seen[e.PID] = &agentSeen{name: e.Name, client: e.Client, project: e.Project, at: e.At,
			start: e.Start, exe: e.Exe, noInput: e.NoInput, noSpeech: e.NoSpeech, custom: e.Custom, session: e.Session}
	}
	a.dirty = true // drop what did not survive
	a.saveLocked()
}

// saveLocked writes the sessions when something worth keeping changed (a new
// session, its name, project or switches), not on every hook.
func (a *Agents) saveLocked() {
	if !a.dirty || a.path == "" {
		return
	}
	a.dirty = false
	list := []savedAgent{}
	for pid, s := range a.seen {
		list = append(list, savedAgent{PID: pid, Start: s.start, Exe: s.exe, Name: s.name, Client: s.client,
			Project: s.project, NoInput: s.noInput, NoSpeech: s.noSpeech, Custom: s.custom, Session: s.session, At: s.at})
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	os.MkdirAll(filepath.Dir(a.path), 0o755)
	tmp := a.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, a.path)
	}
}

// SetSwitches sets what the user excluded a session from: dictation (input)
// and speaking (speech). A nil switch stays as it is.
func (a *Agents) SetSwitches(pid uint32, noInput, noSpeech *bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	if s == nil {
		return
	}
	if noInput != nil {
		s.noInput = *noInput
	}
	if noSpeech != nil {
		s.noSpeech = *noSpeech
	}
	a.dirty = true
	a.saveLocked()
}

// NoInput reports whether the user excluded pid from dictation.
func (a *Agents) NoInput(pid uint32) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	return s != nil && s.noInput
}

// NoSpeech reports whether the user muted pid.
func (a *Agents) NoSpeech(pid uint32) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	return s != nil && s.noSpeech
}

// personasOn reports whether sessions take on their characters.
func (s *Service) personasOn() bool {
	return s.Config != nil && s.Config.Serve.Personas != nil && *s.Config.Serve.Personas
}

// persona is the instruction for the character called name, or "" while
// personas are off.
func (s *Service) persona(name string) string {
	if !s.personasOn() || name == "" {
		return ""
	}
	if ch, ok := s.Config.Character(name); ok {
		return ch.Persona()
	}
	return preset.Character{Name: name}.Persona()
}

// characterPreset is the voice of pid's character, "" for the default.
func (s *Service) characterPreset(pid uint32) string {
	if !s.personasOn() || s.Agents == nil {
		return ""
	}
	if ch, ok := s.Config.Character(s.Agents.Name(pid)); ok {
		return ch.Preset
	}
	return ""
}

// FreeName is a character name no session has yet.
func (a *Agents) FreeName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.freeName()
}

// Matches reports whether pid still is the process voice knows: alive, with
// the start time and executable recorded when it first appeared. Windows
// reuses PIDs, and text must not reach a program that took one over.
func (a *Agents) Matches(pid uint32) bool {
	if a == nil || a.Identify == nil {
		return false
	}
	start, exe, ok := a.Identify(pid)
	if !ok {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	if s == nil || s.start == 0 {
		return true // recorded before identities were (or without Identify)
	}
	return start == s.start && strings.EqualFold(exe, s.exe)
}
