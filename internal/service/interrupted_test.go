package service

import (
	"context"
	"testing"

	"voice/internal/preset"
)

// Dictating over a session's speech is said once in its next poll, and only
// to the sessions whose speech was cut off.
func TestPollInterrupted(t *testing.T) {
	ctx := context.Background()
	s := &Service{Config: &preset.Config{}, Agents: &Agents{}}
	poll := func(session string) SessionResult {
		t.Helper()
		res, err := s.Session(ctx, SessionArgs{Action: "poll", Session: session, State: "busy"})
		if err != nil {
			t.Fatal(err)
		}
		return res.(SessionResult)
	}
	for _, r := range []SessionArgs{{Session: "a", PID: 11, Name: "Mira"}, {Session: "b", PID: 12, Name: "Tobias"}} {
		r.Action = "register"
		if _, err := s.Session(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if poll("a").Interrupted {
		t.Fatal("interrupted before anything happened")
	}
	s.Agents.Interrupted(11, 999) // 999: no such session, skipped
	if !poll("a").Interrupted {
		t.Fatal("the cut-off session was not told")
	}
	if poll("a").Interrupted {
		t.Fatal("told twice")
	}
	if poll("b").Interrupted {
		t.Fatal("a session that was not speaking was told")
	}
	var none *Agents
	none.Interrupted(11) // no sessions kept: nothing happens
}
