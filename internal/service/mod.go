package service

import (
	"context"
	"strings"
	"time"

	"voice/internal/core"
)

// ModArgs is a Claude Code mod asking for what was dictated to its session.
type ModArgs struct {
	Session string `json:"session"`
	// Idle: the mod saw the session's turn end (or none start); it knows that
	// better than the hooks, which never hear of an interrupted turn.
	Idle bool `json:"idle"`
	// Character, when set, makes the session that character (the mod's /character).
	Character string `json:"character,omitempty"`
	// Question: a question is open in the session (a dialog waiting for the
	// user), so dictation goes to it now, though the turn is running.
	Question bool `json:"question,omitempty"`
	// Say, when set, is a question the session puts to the user; with voice
	// replies on, voice reads it aloud as that session.
	Say string `json:"say,omitempty"`
}

// ModResult is what the mod submits as the user's prompt, and the name it shows.
type ModResult struct {
	Name     string   `json:"name"`
	Messages []string `json:"messages"`
	// Characters lists the characters' names, for the mod's picker.
	Characters []string `json:"characters"`
}

// modFor is how long a mod counts as running after it last asked.
const modFor = 10 * time.Second

// Mod hands an idle session's dictated messages to its mod, which submits
// them as a prompt: the one way to start a turn in a session that is not on
// screen. While a turn runs the messages stay for the hooks, which pass them
// into the running turn.
func (s *Service) Mod(ctx context.Context, a ModArgs) (any, error) {
	if s.Agents == nil || a.Session == "" {
		return ModResult{Messages: []string{}}, nil
	}
	ag := s.Agents
	ag.mu.Lock()
	var st *agentSeen
	var pid uint32
	for p, x := range ag.seen {
		if strings.EqualFold(x.session, a.Session) && (st == nil || x.at.After(st.at)) {
			st, pid = x, p
		}
	}
	if st == nil {
		ag.mu.Unlock()
		return ModResult{Messages: []string{}}, nil // no hook of the session reached voice yet
	}
	st.modAt = time.Now()
	// The question is read aloud unless the agent has just spoken (it said it itself).
	if a.Say != "" && s.voiceReplies() && s.Speaker != nil && core.SinceAsked(pid) > 30*time.Second {
		ag.mu.Unlock()
		// A question in another language than the voice's is announced, not read.
		lang := s.sessionLang(pid)
		if text := s.questionText(a.Say, lang); text != "" {
			s.Speak(ctx, SpeakArgs{Text: text, Intent: core.IntentQuestion, Lang: lang, PID: pid, Source: "hook"})
		}
		ag.mu.Lock()
	}
	if a.Character != "" {
		ag.mu.Unlock()
		ch, ok := s.Config.Character(a.Character)
		if !ok {
			return nil, userErr("no character %q", a.Character)
		}
		// The persona goes out with the session's next prompt.
		return ModResult{Name: ag.SetName(pid, ch.Name), Messages: []string{}, Characters: s.characterNames()}, nil
	}
	if a.Idle {
		st.busyAt = time.Time{}
	}
	out := ModResult{Name: st.name, Messages: []string{}, Characters: s.characterNames()}
	if a.Question && len(st.queue) > 0 {
		out.Messages, st.queue = st.queue, nil
	} else if a.Idle && len(st.queue) > 0 {
		out.Messages, st.queue = st.queue, nil
		st.busyAt = time.Now() // the prompt starts a turn
	}
	report := func() {}
	if len(out.Messages) > 0 {
		report = ag.changed(true)
	}
	ag.mu.Unlock()
	report()
	return out, nil
}

// HasMod reports whether pid's session runs voice's Claude Code mod, which
// submits dictation itself: then dictation waits for it instead of typing.
func (a *Agents) HasMod(pid uint32) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	return s != nil && (s.waiting > 0 || !s.modAt.IsZero() && time.Since(s.modAt) < modFor)
}

// CodexThread is the Codex session (thread) id pid's hooks reported, or ""
// for an agent that is not Codex or has no hooks yet.
func (a *Agents) CodexThread(pid uint32) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seen[pid]
	if s == nil || clientKind(s.client) != "codex" {
		return ""
	}
	return s.session
}

func (s *Service) characterNames() []string {
	names := []string{}
	if s.Config != nil {
		for _, ch := range s.Config.Characters {
			names = append(names, ch.Name)
		}
	}
	return names
}
