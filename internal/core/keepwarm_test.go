package core

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"voice/internal/engine"
	"voice/internal/preset"
)

// warmEngine stands for a GPU voice with a server: Warm loads it, Touch
// reaches it only while loaded.
type warmEngine struct {
	mu      sync.Mutex
	loaded  bool
	warms   int
	touches int
}

var warmEng = &warmEngine{}

func (e *warmEngine) Voices(context.Context) ([]engine.VoiceInfo, error) { return nil, nil }
func (e *warmEngine) Close() error                                       { return nil }
func (e *warmEngine) Synthesize(context.Context, engine.Request, func(engine.Chunk) error) error {
	return nil
}
func (e *warmEngine) Warm(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.loaded = true
	e.warms++
	return nil
}
func (e *warmEngine) Touch(context.Context) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		e.touches++
	}
	return e.loaded
}
func (e *warmEngine) state() (bool, int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loaded, e.warms, e.touches
}

func init() {
	engine.Register("warmtest", func(string, engine.Config) (engine.Engine, error) { return warmEng, nil })
}

func TestKeepWarm(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "config.toml")
	os.WriteFile(conf, []byte("default_preset = \"gpu\"\n[engines.warm]\ntype = \"warmtest\"\n[engines.cpu]\ntype = \"turntest\"\n"+
		"[presets.gpu]\nengine = \"warm\"\nchime = \"none\"\n[presets.cpu]\nengine = \"cpu\"\nchime = \"none\"\n"), 0o644)
	cfg, err := preset.Load(conf)
	if err != nil {
		t.Fatal(err)
	}
	sp := NewSpeaker(cfg)
	t.Cleanup(sp.Close)
	warmEng.mu.Lock()
	warmEng.loaded, warmEng.warms, warmEng.touches = false, 0, 0
	warmEng.mu.Unlock()

	// The janitor's touch neither makes nor loads a voice nobody used.
	if name, gpu := sp.KeepWarm("gpu", false); name != "warm" || gpu {
		t.Fatalf("touch before any use: %q %v", name, gpu)
	}
	if loaded, warms, _ := warmEng.state(); loaded || warms != 0 {
		t.Fatal("a touch loaded the voice")
	}

	// register with keep_warm loads it ("" is the active preset, gpu).
	if name, gpu := sp.KeepWarm("", true); name != "warm" || !gpu {
		t.Fatalf("keep warm at register: %q %v", name, gpu)
	}
	deadline := time.Now().Add(2 * time.Second)
	for loaded, _, _ := warmEng.state(); !loaded; loaded, _, _ = warmEng.state() {
		if time.Now().After(deadline) {
			t.Fatal("the voice did not load")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Loaded, a touch reaches the server and the engine counts as used, so
	// voice's own idle unloading leaves it.
	sp.mu.Lock()
	sp.used["warm"] = time.Now().Add(-time.Hour)
	sp.mu.Unlock()
	sp.KeepWarm("gpu", false)
	if _, _, touches := warmEng.state(); touches == 0 {
		t.Fatal("the loaded voice was not touched")
	}
	for _, name := range sp.UnloadIdle(time.Minute) {
		if name == "warm" {
			t.Fatal("a voice kept warm was unloaded for being idle")
		}
	}

	// Freed meanwhile (the GPU memory budget), it stays down until it speaks.
	warmEng.mu.Lock()
	warmEng.loaded = false
	warmEng.mu.Unlock()
	sp.KeepWarm("gpu", false)
	time.Sleep(20 * time.Millisecond)
	if loaded, warms, _ := warmEng.state(); loaded || warms != 1 {
		t.Fatalf("a freed voice was loaded again by a touch (warms %d)", warms)
	}

	// A CPU voice has no server to keep.
	if name, gpu := sp.KeepWarm("cpu", true); name != "cpu" || gpu {
		t.Fatalf("cpu voice: %q %v", name, gpu)
	}
	if _, gpu := sp.KeepWarm("nope", true); gpu {
		t.Fatal("an unknown preset was kept warm")
	}
}
