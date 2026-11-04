package service

import (
	"context"
	"testing"
	"time"

	"voice/internal/preset"
)

func TestSessionAPI(t *testing.T) {
	ctx := context.Background()
	s := &Service{Config: &preset.Config{}, Agents: &Agents{}}
	call := func(a SessionArgs) SessionResult {
		t.Helper()
		res, err := s.Session(ctx, a)
		if err != nil {
			t.Fatalf("%s: %v", a.Action, err)
		}
		return res.(SessionResult)
	}

	if _, err := s.Session(ctx, SessionArgs{Action: "register", Session: "a1"}); err == nil {
		t.Fatal("register without pid succeeded")
	}
	if _, err := s.Session(ctx, SessionArgs{Action: "poll", Session: "nope"}); err == nil {
		t.Fatal("poll of an unknown session succeeded")
	}

	reg := call(SessionArgs{Action: "register", Session: "a1", PID: 901, Client: "my-agent", Project: "notes", Name: "Mira"})
	if reg.Name != "Mira" {
		t.Fatalf("name %q", reg.Name)
	}
	if !s.Agents.HasMod(901) {
		t.Fatal("a registered session must take dictation itself (not typed)")
	}
	info := s.Agents.Info()
	if len(info) != 1 || info[0].Client != "my-agent" || info[0].Project != "notes" {
		t.Fatalf("info %+v", info)
	}

	// Dictated while busy: a busy poll gets it as part of the turn.
	s.Agents.Enqueue(901, "also check the tests")
	if got := call(SessionArgs{Action: "poll", Session: "a1", State: "busy"}); len(got.Messages) != 1 || got.Messages[0] != "also check the tests" {
		t.Fatalf("busy poll %+v", got)
	}
	if !s.Agents.Busy(901) {
		t.Fatal("a busy poll keeps the session busy")
	}

	// Idle: a message is the next prompt, and the turn it starts counts as busy.
	if got := call(SessionArgs{Action: "poll", Session: "a1", State: "idle"}); len(got.Messages) != 0 || s.Agents.Busy(901) {
		t.Fatalf("idle poll without messages %+v busy=%v", got, s.Agents.Busy(901))
	}
	s.Agents.Enqueue(901, "write the summary")
	if got := call(SessionArgs{Action: "poll", Session: "a1"}); len(got.Messages) != 1 || !s.Agents.Busy(901) {
		t.Fatalf("idle poll %+v busy=%v", got, s.Agents.Busy(901))
	}

	if _, err := s.Session(ctx, SessionArgs{Action: "poll", Session: "a1", State: "sleeping"}); err == nil {
		t.Fatal("unknown state accepted")
	}
	if _, err := s.Session(ctx, SessionArgs{Action: "event", Session: "a1", Event: "lunch"}); err == nil {
		t.Fatal("unknown event accepted")
	}

	// Polling is not activity: unaddressed dictation goes to the session the
	// user works with, not to whichever agent polled last.
	time.Sleep(20 * time.Millisecond) // Windows clocks can give two calls the same time
	s.Agents.Seen(902, "claude", "app")
	call(SessionArgs{Action: "poll", Session: "a1", State: "idle"})
	if r := s.Agents.Recent(); len(r) < 2 || r[0] != 902 {
		t.Fatalf("a poll made the session most recent: %v", r)
	}

	if _, err := s.Session(ctx, SessionArgs{Action: "end", Session: "a1"}); err != nil {
		t.Fatal(err)
	}
	if info := s.Agents.Info(); len(info) != 1 || info[0].PID != 902 {
		t.Fatalf("end keeps the session: %+v", info)
	}
}

func TestSendLoopGuard(t *testing.T) {
	ag := &Agents{}
	for i := 0; i < sendLimit; i++ {
		if !ag.allowSend(1, 2) && !ag.allowSend(2, 1) {
			t.Fatalf("message %d refused", i)
		}
	}
	if ag.allowSend(2, 1) {
		t.Fatal("a fifth message within a minute, either way, went through")
	}
	if !ag.allowSend(1, 3) {
		t.Fatal("another pair counts on its own")
	}
}
