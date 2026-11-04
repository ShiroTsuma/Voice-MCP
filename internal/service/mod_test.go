package service

import (
	"context"
	"slices"
	"testing"

	"voice/internal/preset"
)

func TestModTakesAnIdleSessionsMessages(t *testing.T) {
	s := &Service{Agents: &Agents{}}
	ctx := context.Background()
	mod := func(idle bool) ModResult {
		res, _ := s.Mod(ctx, ModArgs{Session: "abc", Idle: idle})
		return res.(ModResult)
	}
	if got := mod(true); got.Name != "" || len(got.Messages) != 0 {
		t.Fatalf("unknown session: %+v", got)
	}
	s.Agent(ctx, AgentArgs{PID: 4, Event: "busy", Session: "abc"})
	s.Agents.Enqueue(4, "run the tests")
	if got := mod(false); len(got.Messages) != 0 || got.Name == "" {
		t.Fatalf("a running turn's messages went to the mod: %+v", got)
	}
	if !s.Agents.HasMod(4) {
		t.Fatal("the mod is not seen")
	}
	// The turn was interrupted: no Stop hook, but the mod knows it is idle.
	if got := mod(true); !slices.Equal(got.Messages, []string{"run the tests"}) || !s.Agents.Busy(4) {
		t.Fatalf("idle: %+v, busy %v", got, s.Agents.Busy(4))
	}
	if got := mod(true); len(got.Messages) != 0 {
		t.Fatalf("delivered twice: %v", got.Messages)
	}
	// A subagent's hook never takes over the session id.
	s.Agent(ctx, AgentArgs{PID: 4, Event: "busy", Session: "sub", Subagent: true})
	if got := mod(true); got.Name == "" {
		t.Fatal("the session id was replaced by a subagent's")
	}
}

func TestModSwitchesTheCharacter(t *testing.T) {
	cfg := &preset.Config{Characters: []preset.Character{{Name: "Annie"}, {Name: "Rebekka"}}}
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Annie", "Rebekka"} }}}
	ctx := context.Background()
	s.Agent(ctx, AgentArgs{PID: 6, Client: "claude", Event: "idle", Session: "s6"})
	res, err := s.Mod(ctx, ModArgs{Session: "s6", Character: "rebekka"})
	if err != nil || res.(ModResult).Name != "Rebekka" || s.Agents.Name(6) != "Rebekka" {
		t.Fatalf("switch: %+v %v", res, err)
	}
	if got := res.(ModResult).Characters; len(got) != 2 {
		t.Fatalf("characters %v", got)
	}
	if _, err := s.Mod(ctx, ModArgs{Session: "s6", Character: "Nobody"}); err == nil {
		t.Fatal("an unknown character was taken")
	}
}

func TestModAnswersAnOpenQuestionWhileBusy(t *testing.T) {
	s := &Service{Agents: &Agents{}}
	ctx := context.Background()
	s.Agent(ctx, AgentArgs{PID: 9, Client: "claude", Event: "busy", Session: "q1"})
	s.Agents.Enqueue(9, "the second one")
	if res, _ := s.Mod(ctx, ModArgs{Session: "q1"}); len(res.(ModResult).Messages) != 0 {
		t.Fatal("a busy session's dictation went to the mod without a question")
	}
	res, _ := s.Mod(ctx, ModArgs{Session: "q1", Question: true})
	if got := res.(ModResult).Messages; len(got) != 1 || got[0] != "the second one" {
		t.Fatalf("open question got %v", got)
	}
}
