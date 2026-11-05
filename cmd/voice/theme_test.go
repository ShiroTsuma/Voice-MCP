package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallTheme(t *testing.T) {
	src, dir := t.TempDir(), t.TempDir()
	story := filepath.Join(src, "storybook")
	os.MkdirAll(filepath.Join(story, "img"), 0o755)
	os.WriteFile(filepath.Join(story, "theme.css"), []byte("body{}"), 0o644)
	os.WriteFile(filepath.Join(story, "img", "tavern.jpg"), []byte("x"), 0o644)

	if n, err := installTheme(dir, story, ""); err != nil || n != "storybook" {
		t.Fatalf("install: %q %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "storybook", "img", "tavern.jpg")); err != nil {
		t.Fatal("pictures not copied")
	}
	// Again under another name, then over itself: the old copy is replaced.
	os.WriteFile(filepath.Join(story, "theme.css"), []byte("body{color:red}"), 0o644)
	if _, err := installTheme(dir, story, "story"); err != nil {
		t.Fatal(err)
	}
	if _, err := installTheme(dir, story, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "storybook", "theme.css")); string(b) != "body{color:red}" {
		t.Fatalf("not replaced: %s", b)
	}
	if got := installedThemes(dir); len(got) != 2 || got[0] != "story" || got[1] != "storybook" {
		t.Fatalf("list: %v", got)
	}
	if _, err := installTheme(dir, src, ""); err == nil {
		t.Error("a folder without theme.css was installed")
	}
	if _, err := installTheme(dir, story, "../out"); err == nil {
		t.Error("a name leaving the themes folder was taken")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Errorf("leftover %s", e.Name())
		}
	}
}
