package listen

import (
	"strings"
	"testing"
)

// Words two passes in a row agree on stay as shown; later passes only add
// words after them, and a pass that decides them otherwise is not shown.
func TestLocalAgreement(t *testing.T) {
	passes := []string{
		"Anana",
		"Anana okay I'm",
		"and Anna okay I'm now", // decides the start otherwise: not shown
		"Anana, okay I'm now talking",
		"Anana okay I'm now talking to you",
	}
	var held, prev []string
	shown := ""
	var history []string
	for _, p := range passes {
		words := strings.Fields(p)
		var ok bool
		held, ok = agreed(held, prev, words)
		prev = words
		if ok {
			shown = strings.Join(words, " ")
		}
		history = append(history, shown)
		if !strings.HasPrefix(shown, "Anana") {
			t.Fatalf("after %q: %q", p, shown)
		}
	}
	if history[2] != history[1] {
		t.Fatalf("a pass that heard the start otherwise changed the text: %q", history[2])
	}
	if got := strings.Join(held, " "); got != "Anana okay I'm now talking" {
		t.Fatalf("held %q", got)
	}
	if shown != "Anana okay I'm now talking to you" {
		t.Fatalf("shown %q", shown)
	}

	// The phrase's final pass keeps the held words when it begins with them,
	// and wins when it heard the start otherwise.
	if got := keepHeld([]string{"Anana", "okay"}, "anana, okay then"); got != "Anana okay then" {
		t.Fatalf("keep: %q", got)
	}
	if got := keepHeld([]string{"Anana", "okay"}, "and Anna okay then"); got != "and Anna okay then" {
		t.Fatalf("final wins: %q", got)
	}
}
