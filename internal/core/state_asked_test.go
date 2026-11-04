package core

import (
	"testing"
	"time"
)

func TestSinceAsked(t *testing.T) {
	SetDataDir(t.TempDir())
	t.Cleanup(func() { statePath, cacheDir = "", "" })
	if d := SinceAsked(4242); d < time.Hour {
		t.Fatalf("never asked: %v", d)
	}
	MarkAsked(4242)
	if d := SinceAsked(4242); d > time.Second {
		t.Fatalf("just asked: %v", d)
	}
	if d := SinceAsked(4243); d < time.Hour {
		t.Fatalf("another agent: %v", d)
	}
}
