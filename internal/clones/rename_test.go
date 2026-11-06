package clones

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameKeepsEverything(t *testing.T) {
	old := Dir
	Dir = t.TempDir()
	defer func() { Dir = old }()
	fakeVoice(t, "margot-a", "Normal")
	os.WriteFile(filepath.Join(dir("margot-a"), "ref.txt"), []byte("Production is down.\n"), 0o644)
	if err := SetDescription("margot-a", "  calm   on-call engineer "); err != nil {
		t.Fatal(err)
	}

	p, err := Rename("margot-a", "margot")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "margot" || p.Group != "Normal" || p.Description != "calm on-call engineer" || p.Text != "Production is down." {
		t.Errorf("renamed voice: %+v", p)
	}
	if _, err := Get("margot-a"); err == nil {
		t.Error("the old name still exists")
	}

	fakeVoice(t, "iris", "")
	if _, err := Rename("margot", "iris"); err == nil {
		t.Error("renaming onto an existing voice: no error")
	}
	if _, err := Rename("margot", "Bad Name"); err == nil {
		t.Error("an invalid name is accepted")
	}

	// About: the user's description, else the design's.
	if (Profile{Designed: "a raspy woman"}).About() != "a raspy woman" || (Profile{Designed: "x", Description: "mine"}).About() != "mine" {
		t.Error("About picks the wrong description")
	}
}
