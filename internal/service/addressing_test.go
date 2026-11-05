package service

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A file of the user's adds words (another language here) to the built-in
// ones; a broken one is reported and skipped.
func TestUserAddressingWords(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "de.json"), []byte(`{"openers":["genau"],"everyone":["alle zusammen"]}`), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{`), 0o644)
	defer LoadAddressing("")
	if errs := LoadAddressing(dir); len(errs) != 1 {
		t.Fatalf("errors %v", errs)
	}
	agents := []AgentInfo{{PID: 7, Name: "Clara"}, {PID: 8, Name: "Luna"}}
	if to, _, _ := Route("Genau, Clara. Mach das.", agents); !slices.Equal(to, []uint32{7}) {
		t.Fatalf("opener: %v", to)
	}
	if to, _, all := Route("Alle zusammen: Pause", agents); !all || len(to) != 2 {
		t.Fatalf("everyone: %v %v", to, all)
	}
	if to, _, _ := Route("Sure, Clara. Remove them.", agents); !slices.Equal(to, []uint32{7}) {
		t.Fatalf("built-in words kept: %v", to)
	}
}
