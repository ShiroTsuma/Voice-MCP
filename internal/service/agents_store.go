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
	a.loadTranscriptLocked()
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

// transcriptPath keeps the transcript next to the agents file, so it
// survives a restart of voice.
func (a *Agents) transcriptPath() string {
	if a.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.path), "transcript.json")
}

func (a *Agents) loadTranscriptLocked() {
	b, err := os.ReadFile(a.transcriptPath())
	if err != nil {
		return
	}
	var lines []Line
	if json.Unmarshal(b, &lines) != nil {
		return
	}
	if n := len(lines); n > transcriptSize {
		lines = lines[n-transcriptSize:]
	}
	a.transcript = append(lines, a.transcript...)
}

// saveTranscriptLocked writes the transcript after each new line (at most
// transcriptSize short lines).
func (a *Agents) saveTranscriptLocked() {
	path := a.transcriptPath()
	if path == "" {
		return
	}
	b, err := json.Marshal(a.transcript)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, path)
	}
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
	ch, ok := s.Config.Character(name)
	if !ok {
		ch = preset.Character{Name: name}
	}
	return ch.PersonaAloud(s.Config.ImmersionOf(ch), s.Config.AloudOf(ch))
}

// aloudFor is how much of a reply the session called name says aloud: its
// character's choice, or the general one when it has none or characters are off.
func (s *Service) aloudFor(name string) int {
	if s.Config == nil {
		return preset.DefaultAloud
	}
	if ch, _ := s.sessionCharacter(name); ch != nil {
		return s.Config.AloudOf(*ch)
	}
	return s.Config.AloudOf(preset.Character{})
}

// takeAloudNote tells pid's agent how much of each reply to say aloud: at the
// start (start) when that is more than a sentence or two, and once after it
// changed (another level, or another character with its own); "" otherwise.
func (s *Service) takeAloudNote(pid uint32, start bool) string {
	if s.Agents == nil || pid == 0 {
		return ""
	}
	level := s.aloudFor(s.Agents.Name(pid))
	ag := s.Agents
	ag.mu.Lock()
	defer ag.mu.Unlock()
	st := ag.seen[pid]
	if st == nil {
		return ""
	}
	told := st.toldLong
	if told == 0 {
		told = preset.AloudShort // what every agent hears without a note
	}
	st.toldLong = level
	switch {
	case start:
		return preset.AloudNote(level)
	case told == level:
		return ""
	}
	return preset.AloudChanged(level)
}

// speechChars is how long one spoken line may be for pid: a narrative or a
// session that says most of its replies aloud is not cut short; 0 uses
// max_chars.
func (s *Service) speechChars(pid uint32) int {
	if s.Agents == nil {
		return 0
	}
	if s.Agents.Style(pid) == StyleNarrative || pid != 0 && s.aloudFor(s.Agents.Name(pid)) > preset.AloudShort {
		return narrativeChars
	}
	return 0
}

// sessionCharacter is the character a session called name plays and every
// how many user messages it is recalled; nil while characters are off.
func (s *Service) sessionCharacter(name string) (*preset.Character, int) {
	if !s.personasOn() || name == "" {
		return nil, 0
	}
	ch, ok := s.Config.Character(name)
	if !ok {
		ch = preset.Character{Name: name}
	}
	return &ch, s.Config.GuidanceOf(ch)
}

// replyAloud tells an agent how to answer aloud what the user dictated: as
// much as the user wants said aloud, else as briefly or as fully as its
// character speaks (in lang, "Polish").
func (s *Service) replyAloud(name, lang string) string {
	const tail = ", no code or lists; details stay on screen. If you ask them something, say the question aloud too."
	long := " in natural spoken sentences, in " + lang + "; code, paths, logs, raw output and long lists stay on screen (say in a few words what they hold). " +
		"Send a long answer in several voice_speak calls with the same turn. If you ask them something, say the question aloud too."
	switch s.aloudFor(name) {
	case preset.AloudMost:
		return "The user is talking to you by voice. Besides your written reply, say its substance aloud with the voice_speak tool (what you did, found or decided, and what you need from them)," + long
	case preset.AloudNearlyAll:
		return "The user is talking to you by voice. Besides your written reply, say nearly all of it aloud with the voice_speak tool, as you would tell it in person," + long
	}
	level := 0
	if ch, _ := s.sessionCharacter(name); ch != nil {
		level = s.Config.ImmersionOf(*ch)
	}
	switch {
	case level >= preset.ImmersionFull:
		return "The user is talking to you by voice. Besides your written reply, say aloud with the voice_speak tool what your character would say to them, as much as it would, in " + lang + tail
	case level == preset.ImmersionVoice:
		return "The user is talking to you by voice. Besides your written reply, say its key point aloud with the voice_speak tool: one or two short sentences in your character's voice and attitude, in " + lang + tail
	}
	return "The user is talking to you by voice. Besides your written reply, say its key point aloud with the voice_speak tool: one or two short sentences, in " + lang + tail
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

// Client is pid's client kind (claude, codex) or, for another agent, the
// name it registered with.
func (a *Agents) Client(pid uint32) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.seen[pid]; s != nil {
		if k := clientKind(s.client); k != "" {
			return k
		}
		return s.client
	}
	return ""
}
