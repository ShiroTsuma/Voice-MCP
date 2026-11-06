package service

import (
	"context"
	"strings"
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

// A cut-off line keeps how much of it was heard; other lines and
// dictation are left alone.
func TestTranscriptCut(t *testing.T) {
	a := &Agents{}
	a.Record(Line{Kind: "said", ID: "v1", Text: "one two three"})
	a.Record(Line{Kind: "dictated", ID: "v1", Text: "hello"})
	a.Record(Line{Kind: "said", ID: "v2", Text: "four five"})
	a.Cut("v1", 0)
	a.Cut("nope", 3)
	tr := a.Transcript()
	if tr[0].Heard == nil || *tr[0].Heard != 0 {
		t.Fatalf("v1 heard %v, want 0", tr[0].Heard)
	}
	if tr[1].Heard != nil || tr[2].Heard != nil {
		t.Fatal("a line that was not cut got marked")
	}
	var none *Agents
	none.Cut("v1", 1)
}

// Every action Session handles has a route: the daemon serves SessionActions,
// and Session refuses the rest, so an action missing from the list fails here.
func TestSessionStopIsServed(t *testing.T) {
	s := &Service{Config: &preset.Config{}, Agents: &Agents{}}
	if _, err := s.Session(context.Background(), SessionArgs{Action: "stop", Session: "x"}); err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Fatalf("stop is not a served action: %v", err)
	}
	if _, err := s.Session(context.Background(), SessionArgs{Action: "dance", Session: "x"}); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("an unknown action got through: %v", err)
	}
}
