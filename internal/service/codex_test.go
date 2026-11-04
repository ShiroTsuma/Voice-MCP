package service

import (
	"context"
	"testing"
)

func TestCodexConversationsInOneProcess(t *testing.T) {
	s := &Service{Agents: &Agents{}}
	ctx := context.Background()
	s.Agent(ctx, AgentArgs{PID: 3, Client: "codex", Project: "game", Event: "busy", Session: "t1"})
	// The Codex notify and an interrupted turn end the turn but keep what waits.
	s.Agents.Enqueue(3, "later")
	s.Agent(ctx, AgentArgs{PID: 3, Client: "codex", Event: "ended"})
	if s.Agents.Busy(3) || len(s.Agents.Take(3)) != 1 {
		t.Fatal("ended took the queue or left the turn busy")
	}
	if got := s.Agents.CodexThread(3); got != "t1" {
		t.Fatalf("thread %q", got)
	}
	// Another conversation in the same app, another project: not a subagent.
	s.Agent(ctx, AgentArgs{PID: 3, Client: "codex", Project: "shop", Event: "busy", Session: "t2"})
	if got := s.Agents.CodexThread(3); got != "t2" || !s.Agents.Busy(3) {
		t.Fatalf("thread %q busy %v", got, s.Agents.Busy(3))
	}
	if s.Agents.Info()[0].Project != "shop" {
		t.Fatal("project not switched")
	}
	// Claude Code is not Codex.
	s.Agent(ctx, AgentArgs{PID: 4, Client: "claude", Event: "busy", Session: "c1"})
	if s.Agents.CodexThread(4) != "" {
		t.Fatal("a Claude session got a Codex thread")
	}
}
