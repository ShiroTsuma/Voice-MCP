package listen

import (
	"os"
	"path/filepath"
	"testing"

	"voice/internal/engine"
	"voice/internal/preset"
)

func TestSetModelSwitchesLive(t *testing.T) {
	old := engine.Dirs.Models
	defer func() { engine.Dirs.Models = old }()
	engine.Dirs.Models = t.TempDir()
	os.MkdirAll(filepath.Join(engine.Dirs.Models, "whisper"), 0o755)

	cfg := &preset.Config{}
	cfg.Listen.Model = "parakeet" // not installed
	l := New(cfg)
	if l.Available() {
		t.Fatal("a missing model counts as available")
	}
	l.SetModel("whisper", "pl", 2)
	if !l.Available() || l.cfg.Language != "pl" || l.cfg.Threads != 2 {
		t.Fatalf("after SetModel: %+v", l.cfg)
	}
}
