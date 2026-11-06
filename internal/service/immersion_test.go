package service

import (
	"context"
	"strings"
	"testing"

	"voice/internal/preset"
)

// The character is recalled every so many of the user's messages (guidance),
// in a line or in full as strongly as it steers (immersion), and answers aloud
// as fully as it would; with characters off nothing of it reaches the agent.
func TestCharacterGuidanceAndImmersion(t *testing.T) {
	on, two := true, 2
	cfg := &preset.Config{Characters: []preset.Character{
		{Name: "Annie", Traits: []string{"cheerful"}},
		{Name: "Greta", Traits: []string{"grumpy"}, Immersion: preset.ImmersionRolePlay, Guidance: new(int)},
	}}
	cfg.Serve.Personas, cfg.Serve.VoiceReplies, cfg.Serve.CharacterGuidance = &on, &on, &two
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Annie", "Greta"} }}}
	call := func(a AgentArgs) AgentResult {
		res, _ := s.Agent(context.Background(), a)
		return res.(AgentResult)
	}
	if start := call(AgentArgs{PID: 1, Event: "idle", Start: true}); !strings.Contains(start.Persona, "short, but sounds like you") {
		t.Fatalf("voice level persona: %q", start.Persona)
	}
	if r := call(AgentArgs{PID: 1, Event: "busy", Prompt: "one"}); r.Reminder != "" {
		t.Fatalf("recalled after one message: %q", r.Reminder)
	}
	if r := call(AgentArgs{PID: 1, Event: "busy"}); r.Reminder != "" {
		t.Fatal("a tool step counted as a message")
	}
	if r := call(AgentArgs{PID: 1, Event: "busy", Prompt: "two"}); r.Reminder != "Remember: you are Annie (cheerful), in what you write and what you say aloud." {
		t.Fatalf("after two messages: %q", r.Reminder)
	}

	s.Agents.SetName(2, "Greta")
	start := call(AgentArgs{PID: 2, Event: "idle", Start: true})
	if !strings.Contains(start.Persona, "what it would do, you do") || !strings.Contains(start.Persona, "Greta, out of character") || strings.Contains(start.Persona, "keep helping") {
		t.Fatalf("role-play persona: %q", start.Persona)
	}
	for i := 0; i < 5; i++ {
		if r := call(AgentArgs{PID: 2, Event: "busy", Prompt: "x"}); r.Reminder != "" {
			t.Fatal("recalled with guidance 0 (only at the start)")
		}
	}
	if got := s.replyAloud("Greta", "English"); !strings.Contains(got, "as much as it would") {
		t.Fatalf("role-play aloud: %q", got)
	}
	if got := s.replyAloud("Annie", "English"); !strings.Contains(got, "in your character's voice") {
		t.Fatalf("voice aloud: %q", got)
	}

	off := false
	cfg.Serve.Personas = &off
	if got := s.replyAloud("Greta", "English"); strings.Contains(got, "character") {
		t.Fatalf("characters off, aloud: %q", got)
	}
	for i := 0; i < 3; i++ {
		if r := call(AgentArgs{PID: 1, Event: "busy", Prompt: "x"}); r.Reminder != "" || r.Persona != "" {
			t.Fatalf("characters off: %+v", r)
		}
	}
}
