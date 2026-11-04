package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Agents remembers the agent processes that used voice (spoke, listened,
// ran a hook), so dictation can reach the one the user works with: whether
// it is in the middle of a turn, and the dictated messages waiting for it.
type Agents struct {
	mu   sync.Mutex
	seen map[uint32]*agentSeen
	// OnChange learns how many dictated messages wait in all queues, and
	// whether a hook just took some; called without the lock held.
	OnChange func(waiting int, delivered bool)
	// Identify tells a process from a later one with the same PID: its start
	// time and executable; ok is false when it is gone. Set before Load.
	Identify func(pid uint32) (start int64, exe string, ok bool)
	// Names lists the characters' names in order: new sessions take the first
	// free one. Nil uses the built-in names.
	Names func() []string

	// Logf, when set, gets a line for everything voice says and every dictation.
	Logf func(format string, args ...any)

	transcript []Line                    // what was said and dictated, oldest first (Record)
	sends      map[[2]uint32][]time.Time // recent messages between two sessions (send)
	path       string                    // where sessions are saved (Load); empty keeps them in memory
	dirty      bool                      // something worth saving changed
}

// changed reports the queues to OnChange; the caller holds the lock and
// calls the returned function after releasing it.
func (a *Agents) changed(delivered bool) func() {
	if a.OnChange == nil {
		return func() {}
	}
	waiting := 0
	for _, s := range a.seen {
		waiting += len(s.queue)
	}
	fn := a.OnChange
	return func() { fn(waiting, delivered) }
}

type agentSeen struct {
	name    string // what the user calls it when dictating: Annie, Rebekka, ...
	client  string
	project string    // working directory name, for routing by spoken name
	at      time.Time // last activity of any kind
	busyAt  time.Time // last sign of a running turn; zero when idle
	queue   []string  // dictated while busy, for its next hook

	start    int64 // process start time and executable, to recognise it after a restart
	exe      string
	noInput  bool      // the user excluded it from dictation
	noSpeech bool      // the user muted it
	style    string    // how it speaks: "" (short) or "narrative"
	told     string    // the character whose persona the agent last received
	custom   bool      // the name is the user's own, not one from the character list
	session  string    // Claude Code's session id, from its hooks
	modAt    time.Time // the last time its Claude Code mod asked for messages
}

// Names given to sessions, short and far apart for a speech recognizer, in
// Polish and English alike.
var agentNames = []string{"Annie", "Rebekka", "Clara", "Diana", "Fiona", "Greta", "Hanna", "Iris",
	"Julia", "Luna", "Maja", "Nina", "Olga", "Paula", "Sara", "Vera", "Wanda", "Zoe"}

// freeName is the first name no other known agent has.
func (a *Agents) freeName() string {
	used := map[string]bool{}
	for _, s := range a.seen {
		used[strings.ToLower(s.name)] = true
	}
	names := agentNames
	if a.Names != nil {
		if n := a.Names(); len(n) > 0 {
			names = n
		}
	}
	for _, n := range names {
		if !used[strings.ToLower(n)] {
			return n
		}
	}
	return fmt.Sprintf("Agent %d", len(a.seen)+1)
}

// listed reports whether name is one voice hands out (a character's).
func (a *Agents) listed(name string) bool {
	names := agentNames
	if a.Names != nil {
		if n := a.Names(); len(n) > 0 {
			names = n
		}
	}
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// Custom reports whether the user gave pid a name of their own.
func (a *Agents) Custom(pid uint32) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	return s != nil && s.custom
}

// SetName gives pid the name the user calls it by; empty keeps the current
// one. A name another agent has moves to pid. It returns the name pid has.
func (a *Agents) SetName(pid uint32, name string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.get(pid, "", "")
	if name = strings.TrimSpace(name); name != "" {
		for p, o := range a.seen {
			if p != pid && strings.EqualFold(o.name, name) {
				o.name, o.custom = a.freeName(), false
			}
		}
		s.name, s.custom = name, !a.listed(name)
		a.dirty = true
	}
	a.saveLocked()
	return s.name
}

// Name is what the user calls pid.
func (a *Agents) Name(pid uint32) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.seen[pid]; s != nil {
		return s.name
	}
	return ""
}

// busyFor is how long a turn counts as running after its last hook: a turn
// the user interrupts never reports its end.
const busyFor = 10 * time.Minute

func (a *Agents) get(pid uint32, client, project string) *agentSeen {
	if a.seen == nil {
		a.seen = map[uint32]*agentSeen{}
	}
	s := a.seen[pid]
	if s == nil {
		s = &agentSeen{name: a.freeName()}
		if a.Identify != nil {
			s.start, s.exe, _ = a.Identify(pid)
		}
		a.seen[pid] = s
		a.dirty = true
	}
	// Hooks say "claude", the MCP client "claude-code": the same client, not a change.
	if client != "" && s.client != client && (s.client == "" || clientKind(client) == "" || clientKind(client) != clientKind(s.client)) {
		s.client = client
		a.dirty = true
	}
	// The first project sticks: a session's subagents run hooks from their own
	// worktrees under the same process, and must not rename it.
	if project != "" && s.project == "" {
		s.project = project
		a.dirty = true
	}
	s.at = time.Now()
	// Keep the list short: agents come and go over a long-running session.
	// The oldest goes first, but one with nothing to lose (no messages waiting,
	// no switches set by the user) before any other.
	for len(a.seen) > 32 {
		var oldest uint32
		plain := func(x *agentSeen) bool { return len(x.queue) == 0 && !x.noInput && !x.noSpeech && !x.custom }
		for p, x := range a.seen {
			if p == pid {
				continue
			}
			if oldest == 0 || plain(x) && !plain(a.seen[oldest]) || plain(x) == plain(a.seen[oldest]) && x.at.Before(a.seen[oldest].at) {
				oldest = p
			}
		}
		delete(a.seen, oldest)
	}
	return s
}

// Seen records activity of agent process pid; 0 is ignored. A nil Agents
// records nothing.
func (a *Agents) Seen(pid uint32, client, project string) {
	if a == nil || pid == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.get(pid, client, project)
	a.saveLocked()
}

// Forget drops pid, e.g. after its process ended.
func (a *Agents) Forget(pid uint32) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if _, ok := a.seen[pid]; ok {
		delete(a.seen, pid)
		a.dirty = true
		a.saveLocked()
	}
	a.mu.Unlock()
}

// Recent lists the agent processes, most recently active first.
func (a *Agents) Recent() []uint32 {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	pids := make([]uint32, 0, len(a.seen))
	for p := range a.seen {
		pids = append(pids, p)
	}
	sort.Slice(pids, func(i, j int) bool { return a.seen[pids[i]].at.After(a.seen[pids[j]].at) })
	return pids
}

// Info lists the agents with their names and state, most recently active first.
func (a *Agents) Info() []AgentInfo {
	var out []AgentInfo
	for _, pid := range a.Recent() {
		a.mu.Lock()
		if s := a.seen[pid]; s != nil {
			out = append(out, AgentInfo{PID: pid, Name: s.name, Client: s.client, Project: s.project,
				Busy: !s.busyAt.IsZero() && time.Since(s.busyAt) < busyFor, Waiting: len(s.queue), Active: s.at,
				NoInput: s.noInput, NoSpeech: s.noSpeech})
		}
		a.mu.Unlock()
	}
	return out
}

// Busy reports whether pid is in the middle of a turn and runs hooks that
// pick up queued dictation.
func (a *Agents) Busy(pid uint32) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	return s != nil && !s.busyAt.IsZero() && time.Since(s.busyAt) < busyFor
}

// Enqueue keeps text for pid's next hook.
func (a *Agents) Enqueue(pid uint32, text string) {
	a.mu.Lock()
	s := a.get(pid, "", "")
	s.queue = append(s.queue, text)
	report := a.changed(false)
	a.mu.Unlock()
	report()
}

// Take returns and clears what was dictated for pid.
func (a *Agents) Take(pid uint32) []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	s := a.seen[pid]
	if s == nil || len(s.queue) == 0 {
		a.mu.Unlock()
		return nil
	}
	q := s.queue
	s.queue = nil
	report := a.changed(false)
	a.mu.Unlock()
	report()
	return q
}

// AgentArgs is a hook's report of where its agent's turn is.
type AgentArgs struct {
	PID     uint32 `json:"pid"`
	Client  string `json:"client,omitempty"`
	Project string `json:"project,omitempty"`
	// busy: a turn runs (prompt submitted, tool finished); idle: the turn ended
	// and the hook passes messages on; ended: the turn ended, nothing passed on
	// (Codex's notify, an interrupted turn).
	Event string `json:"event"`
	// Subagent: the hook ran for a subagent of the session (Claude Code's agent_id).
	Subagent bool `json:"subagent,omitempty"`
	// Start: a new session (SessionStart) gets its persona whatever was told before.
	Start bool `json:"start,omitempty"`
	// Session: Claude Code's session id, which its mod asks for messages by.
	Session string `json:"session,omitempty"`
	// Prompt: the user's prompt (UserPromptSubmit), to tell a dictated one.
	Prompt string `json:"prompt,omitempty"`
}

// AgentResult is what a hook passes on: dictated messages, and the name the
// user calls its session by.
type AgentResult struct {
	Messages []string `json:"messages"`
	Name     string   `json:"name"`
	// Persona, when set, is the character the agent is to become now; Changed
	// says the user switched it mid-session.
	Persona string `json:"persona,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	// VoiceReply: the user spoke this to the agent (dictated it); with the
	// setting on, the agent is to say its answer aloud too.
	VoiceReply bool `json:"voice_reply,omitempty"`
	// Style is how the session speaks: empty (short) or "narrative", where
	// speech is the content (a story, a scene) and the whole reply is said.
	Style string `json:"style,omitempty"`
}

// Speaking styles of a session (voice_control set_style).
const (
	StyleShort     = "short"
	StyleNarrative = "narrative"
)

// SetStyle sets how a session speaks; short is the default.
func (a *Agents) SetStyle(pid uint32, style string) {
	if a == nil || pid == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.seen[pid]; st != nil {
		if style == StyleShort {
			style = ""
		}
		st.style = style
	}
}

// Style is how a session speaks: "" (short) or StyleNarrative.
func (a *Agents) Style(pid uint32) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if st := a.seen[pid]; st != nil {
		return st.style
	}
	return ""
}

// Agent records a hook's event and hands it the messages dictated for its
// agent, which the hook passes on. A turn that gets messages at its end goes
// on (the Stop hook continues it), so it stays busy.
func (s *Service) Agent(_ context.Context, a AgentArgs) (any, error) {
	if s.Agents == nil || a.PID == 0 {
		return AgentResult{Messages: []string{}}, nil
	}
	ag := s.Agents
	ag.mu.Lock()
	st := ag.get(a.PID, a.Client, a.Project)
	// The Codex app runs all its conversations in one process, and a subagent's
	// hooks carry its parent's session id: another id is another conversation,
	// which becomes the one voice reaches in that process.
	if clientKind(a.Client) == "codex" && a.Session != "" && st.session != "" && !strings.EqualFold(a.Session, st.session) {
		st.session, st.project = a.Session, dirOr(a.Project, st.project)
		ag.dirty = true
	}
	// A subagent runs inside the session's process, often from its own worktree:
	// its hooks show the session works, but the user's words are for the session.
	sub := a.Subagent || a.Project != "" && st.project != "" && !strings.EqualFold(a.Project, st.project)
	if !sub && a.Session != "" && st.session != a.Session {
		st.session = a.Session // a new one after /clear
		ag.dirty = true
	}
	// Only hooks that pass messages on take them (busy: as context, idle: by
	// continuing the turn); a notification or a session (re)start leaves them.
	var queued []string
	if !sub && !a.Start && (a.Event == "busy" || a.Event == "idle") {
		queued, st.queue = st.queue, nil
	}
	switch {
	case a.Event == "busy" || len(queued) > 0:
		st.busyAt = time.Now()
	case (a.Event == "idle" || a.Event == "ended") && !sub:
		st.busyAt = time.Time{}
	}
	if queued == nil {
		queued = []string{}
	}
	res := AgentResult{Messages: queued, Name: st.name, Style: st.style}
	// Dictated words reach the agent: through this hook, or as the prompt it
	// is about to answer (the mod's, Codex's queue, typed into a console).
	if !sub && s.voiceReplies() && (len(queued) > 0 || ag.dictatedLocked(a.PID, a.Prompt)) {
		res.VoiceReply = true
	}
	// Not at a turn's end: a changed character waits for the next prompt or step.
	if !sub && (a.Start || a.Event == "busy") {
		res.Persona, res.Changed = s.personaFor(st, a.Start)
	}
	report := func() {}
	if len(queued) > 0 {
		report = ag.changed(true) // the overlay shows it arriving
	}
	ag.saveLocked()
	ag.mu.Unlock()
	report()
	return res, nil
}

// personaFor is the persona st's agent is to receive now: at the start of a
// session, or once after the user changed its character (changed). It is
// recorded as told only when it goes out, so switching personas on later
// still reaches running sessions. The caller holds the lock.
func (s *Service) personaFor(st *agentSeen, start bool) (persona string, changed bool) {
	if !start && st.told == st.name {
		return "", false
	}
	if persona = s.persona(st.name); persona == "" {
		return "", false
	}
	changed = !start && st.told != ""
	st.told = st.name
	s.Agents.dirty = true
	return persona, changed
}

// takePersona is personaFor for pid, locking.
func (s *Service) takePersona(pid uint32, start bool) (string, bool) {
	if s.Agents == nil || pid == 0 {
		return "", false
	}
	ag := s.Agents
	ag.mu.Lock()
	defer ag.mu.Unlock()
	st := ag.seen[pid]
	if st == nil {
		return "", false
	}
	p, c := s.personaFor(st, start)
	ag.saveLocked()
	return p, c
}
