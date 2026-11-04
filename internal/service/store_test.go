package service

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.json")
	id := func(uint32) (int64, string, bool) { return 42, "claude.exe", true }
	a := &Agents{Identify: id}
	a.Load(path)
	s := &Service{Agents: a}
	s.Agent(context.Background(), AgentArgs{PID: 8, Client: "claude", Event: "idle", Session: "sess-1"})

	b := &Agents{Identify: id} // voice restarted
	b.Load(path)
	b.Enqueue(8, "hello")
	res, _ := (&Service{Agents: b}).Mod(context.Background(), ModArgs{Session: "sess-1", Idle: true})
	if got := res.(ModResult).Messages; len(got) != 1 {
		t.Fatalf("after a restart the mod got %v", got)
	}
}
