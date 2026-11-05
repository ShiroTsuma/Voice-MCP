package core

import (
	"context"
	"errors"
	"testing"
)

func TestEndStates(t *testing.T) {
	for _, c := range []struct {
		err   error
		state string
	}{
		{nil, "done"}, {ErrStopped, "stopped"}, {ErrInterrupted, "interrupted"}, {context.Canceled, "interrupted"},
		{ErrMuted, "muted"}, {errors.New("engine qwen: out of memory"), "error"},
	} {
		it := &item{track: &Track{State: "rendering"}, primary: "qwen"}
		endLocked(it, Result{Engine: "kokoro", Err: c.err})
		if it.track.State != c.state || it.track.Ended == nil {
			t.Errorf("%v: %+v", c.err, it.track)
		}
		if c.state == "error" && it.track.Error == "" {
			t.Errorf("error lost: %+v", it.track)
		}
		if !it.track.Fallback {
			t.Errorf("kokoro for qwen is a fallback: %+v", it.track)
		}
	}
	it := &item{track: &Track{State: "playing"}, primary: "qwen", saveTo: `C:\x.wav`}
	endLocked(it, Result{Engine: "qwen"})
	if it.track.Fallback || it.track.Saved != `C:\x.wav` {
		t.Errorf("saved: %+v", it.track)
	}
}
