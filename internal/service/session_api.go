package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"voice/internal/core"
	"voice/internal/hooks"
)

// The session API lets any agent use voice the way Claude Code and Codex do
// through their hooks and mod: register a conversation, say things in its
// character's voice, take what the user dictates to it, and report the end
// of a turn for an announcement. It is served as /v1/session/<action> and
// documented in docs/http-api.md; a session is the agent's own id for one
// conversation, its process ID ties it to a program that runs.

// SessionArgs is one call of the session API; Action comes from the path.
type SessionArgs struct {
	Action  string `json:"-"`
	Session string `json:"session"`           // the agent's id for this conversation
	PID     uint32 `json:"pid,omitempty"`     // register: the agent's process
	Client  string `json:"client,omitempty"`  // register: the agent program's name
	Project string `json:"project,omitempty"` // register: working directory name, for "my-project: ..."
	Name    string `json:"name,omitempty"`    // register: what the user calls it; empty takes a character
	State   string `json:"state,omitempty"`   // poll: idle (waiting for the user) or busy (working)
	Text    string `json:"text,omitempty"`    // say: what to say; event: the agent's last message or question
	Intent  string `json:"intent,omitempty"`  // say: info, question or attention
	Event   string `json:"event,omitempty"`   // event: done, question or permission
	Tool    string `json:"tool,omitempty"`    // event permission: the tool waiting for approval
	Limit   Count  `json:"limit,omitempty"`   // transcript: how many lines
	Of      string `json:"of,omitempty"`      // transcript: a session's name instead of this session
	To      string `json:"to,omitempty"`      // send: the session to reach, by name

	// As the MCP's voice_speak: how a line is said, its language, whether it
	// cuts in, and for this line only a character's or preset's voice.
	Mood      string `json:"mood,omitempty"`
	Lang      string `json:"lang,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
	Voice     string `json:"voice,omitempty"`
	Turn      string `json:"turn,omitempty"` // say: one reply in pieces (SpeakArgs.Turn)
	// scene, as the MCP's voice_scene: the lines in their voices, where and
	// when, how it is heard, or a file to render it into.
	Lines  SpeakLines `json:"lines,omitempty"`
	Scene  *SceneInfo `json:"scene,omitempty"`
	Mode   string     `json:"mode,omitempty"`
	SaveTo string     `json:"save_to,omitempty"`
	ID     string     `json:"id,omitempty"`    // status: a say's or a scene's id
	Style  string     `json:"style,omitempty"` // style: short or narrative
	Theme  string     `json:"theme,omitempty"` // theme: the reading window's theme for this session ("" the user's)
}

// SessionResult answers register and poll.
type SessionResult struct {
	Name string `json:"name"` // what the user calls the session ("Anna, ...")
	// Persona, when set, is the character the agent is to play from now on.
	Persona string `json:"persona,omitempty"`
	// Messages are what the user dictated to the session. Polled idle, they are
	// the user's next prompt; polled busy, they arrived during the turn.
	Messages []string `json:"messages"`
	// VoiceReply: the user spoke these; with the setting on, answer aloud too.
	VoiceReply bool `json:"voice_reply,omitempty"`
	// Interrupted: the user started dictating while the session spoke (or had
	// speech queued), and voice stopped it. Said once.
	Interrupted bool `json:"interrupted,omitempty"`
	// Others are the other sessions' names, which send takes.
	Others []string `json:"others"`
}

// othersThan lists the names of the sessions other than pid.
func (ag *Agents) othersThan(pid uint32) []string {
	out := []string{}
	for _, x := range ag.Info() {
		if x.PID != pid && x.Name != "" {
			out = append(out, x.Name)
		}
	}
	return out
}

// sessionLocked finds the session by id; the caller holds ag.mu.
func (ag *Agents) sessionLocked(id string) (*agentSeen, uint32) {
	var st *agentSeen
	var pid uint32
	for p, x := range ag.seen {
		if id != "" && strings.EqualFold(x.session, id) && (st == nil || x.at.After(st.at)) {
			st, pid = x, p
		}
	}
	return st, pid
}

// Session runs one session API action.
// endTurn closes the session's open turn (say's turn), when voice speaks here.
func (s *Service) endTurn(pid uint32) {
	if s.Speaker != nil {
		s.Speaker.EndTurn(pid)
	}
}

// SessionActions are the Session API's actions; the daemon serves one route
// per name, and Session takes no other, so an action added to Session without
// a route fails its tests instead of answering 404.
var SessionActions = []string{"register", "poll", "say", "scene", "status", "stop", "style", "theme", "themes", "presets", "event", "send", "end", "transcript"}

func (s *Service) Session(ctx context.Context, a SessionArgs) (any, error) {
	if !slices.Contains(SessionActions, a.Action) {
		return nil, userErr("unknown action %q", a.Action)
	}
	if s.Agents == nil {
		return nil, userErr("voice keeps no sessions here")
	}
	if a.Session = strings.TrimSpace(a.Session); a.Session == "" {
		return nil, userErr("session is required: the agent's own id for this conversation")
	}
	ag := s.Agents
	if a.Action == "register" {
		if a.PID == 0 {
			return nil, userErr("register needs pid: the agent's process ID")
		}
		ag.mu.Lock()
		st := ag.get(a.PID, dirOr(a.Client, "agent"), a.Project)
		st.session, st.modAt = a.Session, time.Now() // it polls: dictation waits for it
		ag.dirty = true
		ag.saveLocked()
		ag.mu.Unlock()
		name := ag.Name(a.PID)
		if a.Name != "" {
			name = ag.SetName(a.PID, a.Name)
		}
		persona, _ := s.takePersona(a.PID, true)
		return SessionResult{Name: name, Persona: persona, Messages: []string{}, Others: ag.othersThan(a.PID)}, nil
	}

	ag.mu.Lock()
	st, pid := ag.sessionLocked(a.Session)
	if st == nil {
		ag.mu.Unlock()
		return nil, userErr("unknown session %q: register it first (it is forgotten when its process ends)", a.Session)
	}
	// Polling keeps the session reachable (modAt) but is not activity: an
	// agent polling every second would otherwise always be the "most recent"
	// session and take every dictation that names no one.
	st.modAt = time.Now()
	if a.Action != "poll" {
		st.at = time.Now()
	}
	client, project := st.client, st.project
	ag.mu.Unlock()

	switch a.Action {
	case "poll":
		ag.mu.Lock()
		out := SessionResult{Name: st.name, Messages: []string{}}
		switch a.State {
		case "busy":
			st.busyAt = time.Now()
			out.Messages, st.queue = append(out.Messages, st.queue...), nil
		case "", "idle":
			st.busyAt = time.Time{}
			defer s.endTurn(pid) // waiting for the user: its reply is complete
			if len(st.queue) > 0 {
				out.Messages, st.queue = append(out.Messages, st.queue...), nil
				st.busyAt = time.Now() // the messages start a turn
			}
		default:
			ag.mu.Unlock()
			return nil, userErr("state is idle or busy, not %q", a.State)
		}
		out.VoiceReply = slices.ContainsFunc(out.Messages, func(m string) bool { return !FromAgent(m) }) && s.voiceReplies()
		out.Interrupted, st.interrupted = st.interrupted, false
		report := func() {}
		if len(out.Messages) > 0 {
			report = ag.changed(true)
		}
		ag.mu.Unlock()
		report()
		out.Persona, _ = s.takePersona(pid, false) // after the user changed its character
		out.Others = ag.othersThan(pid)
		return out, nil
	case "say":
		if strings.TrimSpace(a.Text) == "" {
			return nil, userErr("say needs text")
		}
		return s.Speak(ctx, SpeakArgs{Text: a.Text, Intent: a.Intent, Mood: a.Mood, Lang: a.Lang, Interrupt: a.Interrupt, Voice: a.Voice, Turn: a.Turn, PID: pid, Client: client, Project: project})
	case "scene":
		if len(a.Lines) == 0 {
			return nil, userErr("scene needs lines")
		}
		return s.Speak(ctx, SpeakArgs{Lines: a.Lines, Scene: a.Scene, Mode: a.Mode, SaveTo: a.SaveTo, Lang: a.Lang, PID: pid, Client: client, Project: project})
	case "status":
		return s.Control(ctx, ControlArgs{Action: "status", ID: a.ID, PID: pid, Client: client, Project: project})
	case "stop":
		return s.Control(ctx, ControlArgs{Action: "stop", PID: pid, Client: client, Project: project})
	case "style":
		return s.Control(ctx, ControlArgs{Action: "set_style", Style: a.Style, PID: pid, Client: client, Project: project})
	case "theme":
		return s.Control(ctx, ControlArgs{Action: "set_theme", Theme: a.Theme, PID: pid, Client: client, Project: project})
	case "themes":
		return s.Control(ctx, ControlArgs{Action: "list_themes", PID: pid, Client: client, Project: project})
	case "presets":
		return s.Control(ctx, ControlArgs{Action: "list_presets", PID: pid, Client: client, Project: project})
	case "event":
		return s.sessionEvent(ctx, a, pid, client, project)
	case "send":
		return s.sessionSend(a, pid)
	case "end":
		ag.Forget(pid)
		return "ended", nil
	case "transcript":
		if a.Of != "" {
			return ag.TranscriptFor(pid, a.Of, int(a.Limit)), nil
		}
		return ag.TranscriptFor(pid, "me", int(a.Limit)), nil
	}
	return nil, userErr("unknown action %q", a.Action)
}

// sessionEvent announces a turn's end, a question or a permission prompt the
// way the hooks do for Claude Code and Codex, with the same settings: the
// project first, the last sentences of the agent's message, quiet when muted
// or right after the agent spoke for itself.
func (s *Service) sessionEvent(ctx context.Context, a SessionArgs, pid uint32, client, project string) (any, error) {
	ev := hooks.Event{Client: client, Project: project, Summary: a.Text, Tool: a.Tool}
	switch a.Event {
	case "done":
		ev.Kind, ev.Intent = hooks.Done, core.IntentInfo
		s.endTurn(pid) // the reply is complete: voice stops waiting for more of it
		s.Agents.mu.Lock()
		if st := s.Agents.seen[pid]; st != nil {
			st.busyAt = time.Time{}
		}
		s.Agents.mu.Unlock()
	case "question":
		ev.Kind, ev.Intent = hooks.Input, core.IntentQuestion
	case "permission":
		ev.Kind, ev.Intent = hooks.Permission, core.IntentAttention
	default:
		return nil, userErr("event is done, question or permission, not %q", a.Event)
	}
	quiet := func(why string) (any, error) { return map[string]any{"said": "", "quiet": why}, nil }
	h := s.Config.Hooks
	switch {
	case h.Enabled != nil && !*h.Enabled:
		return quiet("announcements are off")
	case core.SharedMuted():
		return quiet("muted")
	case core.SinceAsked(pid) < 30*time.Second:
		return quiet("the agent spoke for itself")
	}
	lang := s.sessionLang(pid)
	text, cacheable := hooks.Compose(h, ev, lang, s.phraseLang(lang))
	if ev.Kind == hooks.Input && strings.TrimSpace(a.Text) != "" && readableIn(a.Text, lang) {
		text, cacheable = strings.TrimSpace(a.Text), false // the question itself
		if project != "" && (h.AnnounceProject == nil || *h.AnnounceProject) {
			text = project + ". " + text
		}
	}
	if text == "" {
		return quiet("no phrase for " + a.Event)
	}
	if _, err := s.Speak(ctx, SpeakArgs{Text: text, Intent: ev.Intent, Lang: lang, Cache: cacheable, PID: pid, Client: client, Project: project, Source: "hook"}); err != nil {
		return nil, err
	}
	return map[string]any{"said": text}, nil
}

// sessionSend passes a message from one session to another, named the way
// the user names it. It reaches the other agent the way the user's dictation
// does (into its running turn, its mod or Codex's queue), says who sent it,
// and shows in the transcript as a message, not as the user's words.
func (s *Service) sessionSend(a SessionArgs, from uint32) (any, error) {
	if strings.TrimSpace(a.Text) == "" || strings.TrimSpace(a.To) == "" {
		return nil, userErr("send needs to (a session's name) and text")
	}
	if s.DeliverTo == nil {
		return nil, userErr("could not send to %s: messages between sessions need voice's background process, which is not running; ask the user to start voice (its tray icon, or voice serve)", a.To)
	}
	want := normalize(a.To)
	var to uint32
	var names []string
	for _, x := range s.Agents.Info() {
		names = append(names, x.Name)
		if x.PID != from && normalize(x.Name) == want {
			to = x.PID
		}
	}
	if to == 0 {
		return nil, userErr("could not send to %q: no other session has that name; sessions now: %s. Use one of these names; a session that ended is gone, so tell the user", a.To, strings.Join(names, ", "))
	}
	if !s.Agents.allowSend(from, to) {
		return nil, userErr("could not send to %s: %s and %s exchanged %d messages in the last minute; stop here and tell the user instead (a loop guard)", a.To, s.Agents.Name(from), a.To, sendLimit)
	}
	sender := dirOr(s.Agents.Name(from), "another session")
	msg := "Message from " + sender + agentMark + strings.TrimSpace(a.Text)
	if s.Agents.Name(from) != "" {
		// How to answer, so any agent can, not only one told in the message.
		msg += fmt.Sprintf("\n(To answer, use voice_control send with to %q.)", s.Agents.Name(from))
	}
	report, ok := s.DeliverTo(to, msg)
	if !ok {
		return nil, userErr("could not send to %s: %s. Its session may have just closed or be between steps; send again in a moment, or tell the user", s.Agents.Name(to), report)
	}
	s.Agents.Record(Line{Kind: "message", PID: to, From: sender, Text: strings.TrimSpace(a.Text), Outcome: report})
	return map[string]any{"to": s.Agents.Name(to), "delivered": report}, nil
}

// agentMark follows the sender's name in a message from another session; it
// tells the message apart from the user's dictation, which waits in the same queue.
const agentMark = " (another agent session, not the user): "

// FromAgent reports whether a queued message came from another agent session
// (voice_control send), not from the user's dictation.
func FromAgent(msg string) bool {
	return strings.HasPrefix(msg, "Message from ") && strings.Contains(msg, agentMark)
}

// sendLimit messages between the same two sessions in sendWindow, either
// way: two agents answering each other's answers would talk forever.
const (
	sendLimit  = 4
	sendWindow = time.Minute
)

// allowSend records a message from one session to another, unless the pair
// already exchanged sendLimit within sendWindow.
func (ag *Agents) allowSend(from, to uint32) bool {
	if from > to {
		from, to = to, from
	}
	key := [2]uint32{from, to}
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if ag.sends == nil {
		ag.sends = map[[2]uint32][]time.Time{}
	}
	recent := ag.sends[key][:0]
	for _, at := range ag.sends[key] {
		if time.Since(at) < sendWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= sendLimit {
		ag.sends[key] = recent
		return false
	}
	ag.sends[key] = append(recent, time.Now())
	return true
}
