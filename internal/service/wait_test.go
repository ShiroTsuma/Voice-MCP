package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"voice/internal/preset"
)

// waitService is a service with one registered session "a" (pid 11).
func waitService(t *testing.T) *Service {
	t.Helper()
	s := &Service{Config: &preset.Config{}, Agents: &Agents{}}
	if _, err := s.Session(context.Background(), SessionArgs{Action: "register", Session: "a", PID: 11, Name: "Mira"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func waitPoll(t *testing.T, ctx context.Context, s *Service, wait float64) (SessionResult, time.Duration, error) {
	t.Helper()
	start := time.Now()
	res, err := s.Session(ctx, SessionArgs{Action: "poll", Session: "a", State: "idle", Wait: wait})
	if err != nil {
		return SessionResult{}, time.Since(start), err
	}
	return res.(SessionResult), time.Since(start), nil
}

// A poll with wait answers at once when something is waiting, and otherwise
// as soon as something comes: a message, or the user dictating over it.
func TestPollWaitReturnsOnNews(t *testing.T) {
	s := waitService(t)
	ctx := context.Background()

	s.Agents.Enqueue(11, "already here")
	res, took, err := waitPoll(t, ctx, s, 5)
	if err != nil || len(res.Messages) != 1 || took > time.Second {
		t.Fatalf("waiting message: %+v in %v, %v", res, took, err)
	}

	go func() { time.Sleep(50 * time.Millisecond); s.Agents.Enqueue(11, "hello") }()
	res, took, err = waitPoll(t, ctx, s, 5)
	if err != nil || len(res.Messages) != 1 || res.Messages[0] != "hello" || took > 2*time.Second {
		t.Fatalf("message during the wait: %+v in %v, %v", res, took, err)
	}

	go func() { time.Sleep(50 * time.Millisecond); s.Agents.Interrupted(11) }()
	res, took, err = waitPoll(t, ctx, s, 5)
	if err != nil || !res.Interrupted || took > 2*time.Second {
		t.Fatalf("interruption during the wait: %+v in %v, %v", res, took, err)
	}

	// Another session's message wakes the wait, which looks and waits on.
	if _, err := s.Session(ctx, SessionArgs{Action: "register", Session: "b", PID: 12, Name: "Tobias"}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		s.Agents.Enqueue(12, "for b")
		time.Sleep(50 * time.Millisecond)
		s.Agents.Enqueue(11, "for a")
	}()
	res, _, err = waitPoll(t, ctx, s, 5)
	if err != nil || len(res.Messages) != 1 || res.Messages[0] != "for a" {
		t.Fatalf("woken by another session: %+v, %v", res, err)
	}
}

// With nothing to say it answers when the wait ends, which is capped, or when
// the caller goes away; without wait it answers at once as before.
func TestPollWaitEnds(t *testing.T) {
	s := waitService(t)
	ctx := context.Background()

	res, took, err := waitPoll(t, ctx, s, 0.1)
	if err != nil || len(res.Messages) != 0 || took < 100*time.Millisecond || took > time.Second {
		t.Fatalf("empty wait: %+v in %v, %v", res, took, err)
	}
	if res.Name != "Mira" || res.Messages == nil {
		t.Fatalf("an empty answer still names the session and lists no messages: %+v", res)
	}

	if _, took, _ = waitPoll(t, ctx, s, 0); took > 50*time.Millisecond {
		t.Fatalf("a poll without wait took %v", took)
	}

	old := pollWaitMax
	pollWaitMax = 100 * time.Millisecond
	defer func() { pollWaitMax = old }()
	if _, took, _ = waitPoll(t, ctx, s, 60); took > time.Second {
		t.Fatalf("a wait over the cap took %v", took)
	}
	pollWaitMax = old

	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, took, err = waitPoll(t, cctx, s, 5); err != nil || took > 2*time.Second {
		t.Fatalf("a caller that went away: %v, %v", took, err)
	}

	if _, _, err = waitPoll(t, ctx, s, -1); err == nil {
		t.Fatal("a negative wait was taken")
	}
}

// While a poll waits its session is reachable, so dictation waits for it
// instead of being typed, even past the time a finished poll counts for; a
// session ended during the wait answers so.
func TestPollWaitPresence(t *testing.T) {
	s := waitService(t)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() { _, _, err := waitPoll(t, ctx, s, 5); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.Agents.mu.Lock()
		waiting := s.Agents.seen[11].waiting
		s.Agents.seen[11].modAt = time.Now().Add(-time.Hour) // as if long ago
		s.Agents.mu.Unlock()
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the poll never started waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !s.Agents.HasMod(11) {
		t.Fatal("a waiting session is not reachable")
	}

	s.Agents.Forget(11)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "ended") {
			t.Fatalf("a session ended during its wait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ending the session did not end its wait")
	}
}

// A character edited in the settings (Wake, called after they are applied)
// reaches a waiting session at once.
func TestPollWaitWake(t *testing.T) {
	on := true
	cfg := &preset.Config{Characters: []preset.Character{{Name: "Mira", Traits: []string{"cheerful"}}}}
	cfg.Serve.Personas = &on
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Mira"} }}}
	ctx := context.Background()
	if _, err := s.Session(ctx, SessionArgs{Action: "register", Session: "a", PID: 11}); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Agents.mu.Lock()                            // the poll reads the character under this lock
		cfg.Characters[0].Traits = []string{"grumpy"} // what a reload does to the config
		s.Agents.mu.Unlock()
		s.Agents.Wake()
	}()
	res, took, err := waitPoll(t, ctx, s, 5)
	if err != nil || !strings.Contains(res.Persona, "grumpy") || took > 2*time.Second {
		t.Fatalf("edited character during the wait: %q in %v, %v", res.Persona, took, err)
	}
	var none *Agents
	none.Wake()
}
