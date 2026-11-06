package core

import (
	"testing"
	"time"
)

// A cut-off utterance reports how many of its words were heard: none while
// queued, the finished sentences and the share of the one playing, all of
// it once its time has run; a finished one or a file reports nothing.
func TestOnCutWords(t *testing.T) {
	got := make(chan [2]any, 1)
	s := &Speaker{}
	s.OnCut(func(id string, words int) { got <- [2]any{id, words} })
	text := "One two three. Four five six seven." // the second sentence: runes 15..35
	wait := func() [2]any {
		t.Helper()
		select {
		case g := <-got:
			return g
		case <-time.After(2 * time.Second):
			t.Fatal("OnCut not called")
		}
		return [2]any{}
	}

	s.endedLocked(&item{id: "q", text: text}, Result{Err: ErrStopped})
	if g := wait(); g[0] != "q" || g[1] != 0 {
		t.Fatalf("queued: %v, want 0 words", g)
	}

	half := &item{id: "h", text: text}
	half.line = Line{Done: 15, From: 15, To: 35, At: time.Now().Add(-time.Second), Seconds: 2}
	s.endedLocked(half, Result{Err: ErrInterrupted})
	if g := wait(); g[1] != 5 { // "One two three. Four five" (half of "Four five six seven." reaches "five")
		t.Fatalf("halfway: %v, want 5 words", g)
	}

	over := &item{id: "o", text: text}
	over.line = Line{Done: 15, From: 15, To: 35, At: time.Now().Add(-time.Minute), Seconds: 2}
	s.endedLocked(over, Result{Err: ErrMuted})
	if g := wait(); g[1] != 7 {
		t.Fatalf("its time ran out: %v, want all 7 words", g)
	}

	s.endedLocked(&item{id: "d", text: text}, Result{})
	s.endedLocked(&item{id: "f", text: text, saveTo: `C:\x.wav`}, Result{Err: ErrStopped})
	select {
	case g := <-got:
		t.Fatalf("reported %v for a finished line or a file", g)
	case <-time.After(100 * time.Millisecond):
	}
}
