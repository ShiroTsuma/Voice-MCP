package core

import (
	"testing"
	"time"
)

// Two runs of voice a few seconds apart number their utterances apart, so a
// new run's "v...-3" never matches an old run's line in the transcript.
func TestRunTag(t *testing.T) {
	at := time.Date(2026, 11, 7, 14, 40, 0, 0, time.UTC)
	a, b := runTag(at), runTag(at.Add(4*time.Second))
	if a == b || len(a) != 4 || a[3] != '-' {
		t.Fatalf("run tags %q and %q", a, b)
	}
}
