package service

import (
	"context"
	"strings"
	"testing"

	"voice/internal/preset"
)

// How much of a reply is said aloud reaches the agent through the hooks: at
// the start when more than short, once after a change, and with the
// reminders; sessions without a character follow the general level.
func TestAloudThroughHooks(t *testing.T) {
	off, every := false, 2
	most := preset.AloudMost
	cfg := &preset.Config{}
	cfg.Serve.Personas, cfg.Serve.CharacterGuidance, cfg.Serve.CharacterAloud = &off, &every, &most
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Annie"} }}}
	call := func(a AgentArgs) AgentResult {
		res, _ := s.Agent(context.Background(), a)
		return res.(AgentResult)
	}
	if r := call(AgentArgs{PID: 1, Event: "idle", Start: true}); r.Speech != preset.AloudNote(preset.AloudMost) {
		t.Fatalf("start, characters off: %q", r.Speech)
	}
	if r := call(AgentArgs{PID: 1, Event: "busy", Prompt: "one"}); r.Speech != "" {
		t.Fatalf("told again without a change: %q", r.Speech)
	}
	if r := call(AgentArgs{PID: 1, Event: "busy", Prompt: "two"}); r.Speech != preset.AloudNote(preset.AloudMost) || r.Reminder != "" {
		t.Fatalf("reminder without a character: %+v", r)
	}
	if got := s.speechChars(1); got != narrativeChars {
		t.Fatalf("long speech is cut at %d", got)
	}

	nearly := preset.AloudNearlyAll
	cfg.Serve.CharacterAloud = &nearly
	if r := call(AgentArgs{PID: 1, Event: "busy"}); r.Speech != preset.AloudChanged(preset.AloudNearlyAll) {
		t.Fatalf("after a change: %q", r.Speech)
	}
	short := preset.AloudShort
	cfg.Serve.CharacterAloud = &short
	if r := call(AgentArgs{PID: 1, Event: "busy"}); !strings.Contains(r.Speech, "keep it short") {
		t.Fatalf("back to short: %q", r.Speech)
	}
	for i := 0; i < 4; i++ {
		if r := call(AgentArgs{PID: 1, Event: "busy", Prompt: "x"}); r.Speech != "" {
			t.Fatalf("short is reminded: %q", r.Speech)
		}
	}
	if got := s.speechChars(1); got != 0 {
		t.Fatalf("short speech is not cut at max_chars: %d", got)
	}
	if r := call(AgentArgs{PID: 2, Event: "idle", Start: true}); r.Speech != "" {
		t.Fatalf("short at the start: %q", r.Speech)
	}
}

// A character's own level overrides the general one, in its persona, the
// note and the answer to a dictated message.
func TestAloudPerCharacter(t *testing.T) {
	on := true
	cfg := &preset.Config{Characters: []preset.Character{
		{Name: "Annie", Traits: []string{"cheerful"}},
		{Name: "Greta", Traits: []string{"grumpy"}, Aloud: preset.AloudNearlyAll},
	}}
	cfg.Serve.Personas, cfg.Serve.VoiceReplies = &on, &on
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Annie", "Greta"} }}}
	call := func(a AgentArgs) AgentResult {
		res, _ := s.Agent(context.Background(), a)
		return res.(AgentResult)
	}
	annie := call(AgentArgs{PID: 1, Event: "idle", Start: true})
	if annie.Speech != "" || !strings.Contains(annie.Persona, "stays short") {
		t.Fatalf("Annie, general short: %+v", annie)
	}
	s.Agents.SetName(2, "Greta")
	greta := call(AgentArgs{PID: 2, Event: "idle", Start: true})
	if greta.Speech != preset.AloudNote(preset.AloudNearlyAll) || strings.Contains(greta.Persona, "short") {
		t.Fatalf("Greta, nearly all: %+v", greta)
	}
	if got := s.replyAloud("Greta", "English"); !strings.Contains(got, "nearly all of it aloud") || !strings.Contains(got, "same turn") {
		t.Fatalf("Greta answers aloud: %q", got)
	}
	if got := s.replyAloud("Annie", "English"); !strings.Contains(got, "one or two short sentences") {
		t.Fatalf("Annie answers aloud: %q", got)
	}

	// The register answer carries it, and switching to a character with
	// another level says so once.
	out, err := s.Control(context.Background(), ControlArgs{Action: "register", PID: 2})
	if err != nil || !strings.Contains(out.(string), "nearly all of each reply aloud") {
		t.Fatalf("register: %v %v", out, err)
	}
	out, err = s.Control(context.Background(), ControlArgs{Action: "set_character", PID: 2, Character: "Annie"})
	if err != nil || !strings.Contains(out.(string), "keep it short") {
		t.Fatalf("set_character back to short: %v %v", out, err)
	}
	most := preset.AloudMost
	cfg.Serve.CharacterAloud = &most
	if got := s.replyAloud("Annie", "English"); !strings.Contains(got, "say its substance aloud") {
		t.Fatalf("Annie, general most: %q", got)
	}
}

// The Session API gives the same note: at register, and in a poll after a change.
func TestAloudSessionAPI(t *testing.T) {
	ctx := context.Background()
	most := preset.AloudMost
	cfg := &preset.Config{}
	cfg.Serve.CharacterAloud = &most
	s := &Service{Config: cfg, Agents: &Agents{}}
	res, err := s.Session(ctx, SessionArgs{Action: "register", Session: "a", PID: 11, Name: "Mira"})
	if err != nil || res.(SessionResult).Speech != preset.AloudNote(preset.AloudMost) {
		t.Fatalf("register: %+v %v", res, err)
	}
	poll := func() SessionResult {
		res, err := s.Session(ctx, SessionArgs{Action: "poll", Session: "a", State: "busy"})
		if err != nil {
			t.Fatal(err)
		}
		return res.(SessionResult)
	}
	if p := poll(); p.Speech != "" {
		t.Fatalf("poll without a change: %q", p.Speech)
	}
	nearly := preset.AloudNearlyAll
	cfg.Serve.CharacterAloud = &nearly
	if p := poll(); p.Speech != preset.AloudChanged(preset.AloudNearlyAll) {
		t.Fatalf("poll after a change: %q", p.Speech)
	}
	if p := poll(); p.Speech != "" {
		t.Fatalf("told twice: %q", p.Speech)
	}
}
