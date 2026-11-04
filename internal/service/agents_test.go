package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"voice/internal/preset"
)

func TestAgentsRecentFirst(t *testing.T) {
	var a Agents
	a.Seen(10, "codex", "")
	time.Sleep(time.Millisecond)
	a.Seen(20, "claude-code", "")
	a.Seen(0, "ignored", "")
	time.Sleep(time.Millisecond)
	a.Seen(10, "codex", "") // active again
	if got := a.Recent(); !slices.Equal(got, []uint32{10, 20}) {
		t.Fatalf("Recent = %v", got)
	}
	a.Forget(10)
	if got := a.Recent(); !slices.Equal(got, []uint32{20}) {
		t.Fatalf("after Forget: %v", got)
	}
	var none *Agents
	none.Seen(1, "x", "") // nil records nothing
	if none.Recent() != nil {
		t.Fatal("nil Agents listed something")
	}
}

func TestAgentHooksTakeDictation(t *testing.T) {
	s := &Service{Agents: &Agents{}}
	call := func(event string) []string {
		res, err := s.Agent(context.Background(), AgentArgs{PID: 7, Client: "claude", Event: event})
		if err != nil {
			t.Fatal(err)
		}
		return res.(AgentResult).Messages
	}
	if call("busy"); !s.Agents.Busy(7) {
		t.Fatal("a running turn is not busy")
	}
	s.Agents.Enqueue(7, "skip the tests")
	if got := call("busy"); !slices.Equal(got, []string{"skip the tests"}) {
		t.Fatalf("PostToolUse got %v", got)
	}
	if got := call("busy"); len(got) != 0 {
		t.Fatalf("delivered twice: %v", got)
	}
	// Dictated just before the turn ends: Stop gets it and the turn goes on.
	s.Agents.Enqueue(7, "and commit")
	if got := call("idle"); len(got) != 1 || !s.Agents.Busy(7) {
		t.Fatalf("Stop got %v, busy %v", got, s.Agents.Busy(7))
	}
	if call("idle"); s.Agents.Busy(7) {
		t.Fatal("an ended turn stays busy")
	}
}

func TestSubagentHooksLeaveTheSessionsMessages(t *testing.T) {
	s := &Service{Agents: &Agents{}}
	s.Agent(context.Background(), AgentArgs{PID: 9, Project: "MCP-Tools", Event: "busy"})
	s.Agents.Enqueue(9, "for the session")
	for _, sub := range []AgentArgs{{PID: 9, Project: "agent-a1b2", Event: "busy"}, {PID: 9, Project: "MCP-Tools", Event: "idle", Subagent: true}} {
		res, _ := s.Agent(context.Background(), sub)
		if got := res.(AgentResult).Messages; len(got) != 0 {
			t.Fatalf("a subagent took %v", got)
		}
	}
	if !s.Agents.Busy(9) {
		t.Fatal("a subagent's Stop ended the session's turn")
	}
	res, _ := s.Agent(context.Background(), AgentArgs{PID: 9, Project: "MCP-Tools", Event: "busy"})
	if got := res.(AgentResult).Messages; len(got) != 1 {
		t.Fatalf("the session got %v", got)
	}
}

func TestPersonaReachesTheSessionOnceAndAfterAChange(t *testing.T) {
	on := true
	cfg := &preset.Config{Characters: []preset.Character{{Name: "Annie", Traits: []string{"cheerful"}}, {Name: "Rebekka", Traits: []string{"teasing"}}}}
	cfg.Serve.Personas = &on
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Annie", "Rebekka"} }}}
	call := func(a AgentArgs) AgentResult {
		res, _ := s.Agent(context.Background(), a)
		return res.(AgentResult)
	}
	start := call(AgentArgs{PID: 5, Event: "idle", Start: true})
	if start.Name != "Annie" || !strings.Contains(start.Persona, "cheerful") || start.Changed {
		t.Fatalf("start: %+v", start)
	}
	if again := call(AgentArgs{PID: 5, Event: "busy"}); again.Persona != "" {
		t.Fatalf("told twice: %+v", again)
	}
	s.Agents.SetName(5, "Rebekka")
	if end := call(AgentArgs{PID: 5, Event: "idle"}); end.Persona != "" {
		t.Fatal("a change went out at a turn's end")
	}
	next := call(AgentArgs{PID: 5, Event: "busy"})
	if !next.Changed || !strings.Contains(next.Persona, "teasing") {
		t.Fatalf("change: %+v", next)
	}
	off := false
	cfg.Serve.Personas = &off
	if p := call(AgentArgs{PID: 6, Event: "idle", Start: true}); p.Persona != "" || p.Name != "Annie" {
		t.Fatalf("personas off: %+v", p)
	}
}

func TestRegisterKeepsTheUsersOwnName(t *testing.T) {
	cfg := &preset.Config{Characters: []preset.Character{{Name: "Annie"}, {Name: "Rebekka"}}}
	names := func() []string { return []string{cfg.Characters[0].Name, cfg.Characters[1].Name} }
	s := &Service{Config: cfg, Agents: &Agents{Names: names}}
	register := func(pid uint32) string {
		s.Control(context.Background(), ControlArgs{Action: "register", PID: pid})
		return s.Agents.Name(pid)
	}
	s.Agents.SetName(1, "Bob") // the user's own
	if got := register(1); got != "Bob" {
		t.Fatalf("custom name became %s", got)
	}
	if got := register(2); got != "Annie" {
		t.Fatalf("new session: %s", got)
	}
	cfg.Characters[0].Name = "Anna" // renamed in the characters window
	if got := register(2); got != "Rebekka" && got != "Anna" {
		t.Fatalf("a former character's name stayed: %s", got)
	}
}

func TestOnlyBusyAndIdleHooksTakeTheQueue(t *testing.T) {
	s := &Service{Agents: &Agents{}}
	s.Agent(context.Background(), AgentArgs{PID: 4, Event: "busy"})
	s.Agents.Enqueue(4, "wait for me")
	for _, a := range []AgentArgs{{PID: 4, Event: ""}, {PID: 4, Event: "", Start: true}} { // Notification, SessionStart
		res, _ := s.Agent(context.Background(), a)
		if got := res.(AgentResult).Messages; len(got) != 0 {
			t.Fatalf("%+v took %v", a, got)
		}
	}
	res, _ := s.Agent(context.Background(), AgentArgs{PID: 4, Event: "busy"})
	if got := res.(AgentResult).Messages; len(got) != 1 {
		t.Fatalf("the queue was lost: %v", got)
	}
}
