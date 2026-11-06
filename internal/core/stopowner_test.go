package core

import (
	"context"
	"errors"
	"testing"
)

func TestStopOwner(t *testing.T) {
	mk := func(id string, owner uint32) *item {
		return &item{id: id, owner: owner, track: &Track{ID: id, State: "queued"}, done: make(chan Result, 1)}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cur := mk("v1", 7)
	cur.track.State = "playing"
	s := &Speaker{current: cur, cancel: cancel, queue: []*item{mk("v2", 9), mk("v3", 7), mk("v4", 0), mk("v5", 9)}}
	queued := append([]*item(nil), s.queue...)

	if n := s.StopOwner(9); n != 2 {
		t.Fatalf("stopped %d, want the two queued of owner 9", n)
	}
	if ctx.Err() != nil {
		t.Fatal("another session's playing utterance was cut off")
	}
	if len(s.queue) != 2 || s.queue[0].id != "v3" || s.queue[1].id != "v4" {
		t.Fatalf("queue %v, want v3 and v4 in order", ids(s.queue))
	}
	for _, it := range []*item{queued[0], queued[3]} {
		if it.track.State != "stopped" {
			t.Errorf("%s: %s, want stopped", it.id, it.track.State)
		}
		if res := <-it.done; !errors.Is(res.Err, ErrStopped) {
			t.Errorf("%s done with %v", it.id, res.Err)
		}
	}

	if n := s.StopOwner(7); n != 2 {
		t.Fatalf("stopped %d, want the playing one and the queued one of owner 7", n)
	}
	if ctx.Err() == nil || !errors.Is(cur.why, ErrStopped) {
		t.Fatalf("the playing utterance of owner 7 goes on (why %v)", cur.why)
	}
	if len(s.queue) != 1 || s.queue[0].id != "v4" {
		t.Fatalf("queue %v, want v4 alone", ids(s.queue))
	}
	if n := s.StopOwner(3); n != 0 {
		t.Fatalf("an owner with nothing stopped %d", n)
	}
}

// An interruption keeps its own reason; a later stop does not rename it.
func TestCutKeepsFirstReason(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	cur := &item{id: "v1", owner: 1}
	s := &Speaker{current: cur, cancel: cancel}
	s.dropLocked(ErrInterrupted)
	s.cutLocked(ErrStopped)
	if !errors.Is(cur.why, ErrInterrupted) {
		t.Fatalf("why %v, want interrupted", cur.why)
	}
}

func ids(items []*item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.id)
	}
	return out
}
