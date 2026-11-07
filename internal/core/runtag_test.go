package core

import "testing"

// Each run of voice numbers its utterances under its own tag, so a new run's
// "v…-3" never matches an old run's line in the transcript.
func TestRunTag(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		tag := runTag()
		if len(tag) != 5 || tag[4] != '-' {
			t.Fatalf("run tag %q", tag)
		}
		seen[tag] = true
	}
	if len(seen) < 19 {
		t.Fatalf("20 runs share tags: %v", seen)
	}
}
