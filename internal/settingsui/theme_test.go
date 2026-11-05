package settingsui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemePath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "story", "img"), 0o755)
	os.WriteFile(filepath.Join(dir, "story", "theme.css"), []byte("body{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "story", "img", "tavern.jpg"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x"), 0o644)
	for _, ok := range []string{"story/theme.css", "story/img/tavern.jpg"} {
		if _, err := themePath(dir, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "secret.txt", "story", "story/../secret.txt", "../x", "story/img", `story\..\secret.txt`, "C:/x/y", "story/nope.css"} {
		if _, err := themePath(dir, bad); err == nil {
			t.Errorf("%q was served", bad)
		}
	}
	if got := themes(dir); len(got) != 1 || got[0] != "story" {
		t.Errorf("themes: %v", got)
	}
}
