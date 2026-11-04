package autostart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateShortcut(t *testing.T) {
	p := filepath.Join(t.TempDir(), "voice.lnk")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := createShortcut(p, exe); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Fatalf("shortcut not written: %v", err)
	}
}
