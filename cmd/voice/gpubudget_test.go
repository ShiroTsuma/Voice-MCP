package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"voice/internal/engine"
	"voice/internal/preset"
	"voice/sidecar"
)

func TestBudgetSetting(t *testing.T) {
	for v, want := range map[string]string{"": "off", "off": "off", "one": "one", "8": "8", "6 GB": "6", "nonsense": "off"} {
		cfg := &preset.Config{Serve: preset.Serve{GPUBudget: v}}
		if got := budgetSetting(cfg); got != want {
			t.Errorf("%q: %q, want %q", v, got, want)
		}
	}
}

// fakeGPU is a loaded GPU voice's engine that counts its unloads.
type fakeGPU struct {
	loaded   bool
	unloaded int
}

func (f *fakeGPU) Voices(context.Context) ([]engine.VoiceInfo, error) { return nil, nil }
func (f *fakeGPU) Synthesize(context.Context, engine.Request, func(engine.Chunk) error) error {
	return nil
}
func (f *fakeGPU) Close() error                { return nil }
func (f *fakeGPU) Warm(context.Context) error  { f.loaded = true; return nil }
func (f *fakeGPU) Loaded(context.Context) bool { return f.loaded }
func (f *fakeGPU) Touch(context.Context) bool  { return f.loaded }
func (f *fakeGPU) Unload(context.Context)      { f.loaded = false; f.unloaded++ }

// Before a GPU voice loads, the budget frees the one used least recently
// first, only as many as it must; "one" frees them all; a voice too big for
// the budget on its own still loads, with the others freed.
func TestMakeRoom(t *testing.T) {
	old, oldMem := voiceEngine, gpuMemory
	defer func() { voiceEngine, gpuMemory = old, oldMem }()
	gpuMemory = func(context.Context) (float64, float64, bool) { return 0, 0, false } // the budget alone
	var fakes map[string]*fakeGPU
	voiceEngine = func(name string) (engine.Engine, error) {
		if f, ok := fakes[name]; ok {
			return f, nil
		}
		return &fakeGPU{}, nil
	}
	cfg := &preset.Config{}
	g := &gpuBudget{used: map[string]time.Time{}, measured: map[string]float64{}, config: func() *preset.Config { return cfg }}
	reset := func(budget string) {
		cfg.Serve.GPUBudget = budget
		fakes = map[string]*fakeGPU{"chatterbox": {loaded: true}, "omnivoice": {loaded: true}}
		now := time.Now()
		g.used = map[string]time.Time{"chatterbox": now.Add(-time.Minute), "omnivoice": now} // chatterbox used least recently
	}

	reset("10") // 4 + 4 loaded, qwen3 needs 5.5: one must go, the older one
	g.makeRoom(context.Background(), "qwen3")
	if fakes["chatterbox"].unloaded != 1 || fakes["omnivoice"].unloaded != 0 {
		t.Fatalf("10 GB: chatterbox %d, omnivoice %d unloads", fakes["chatterbox"].unloaded, fakes["omnivoice"].unloaded)
	}
	reset("16") // everything fits
	g.makeRoom(context.Background(), "qwen3")
	if fakes["chatterbox"].unloaded+fakes["omnivoice"].unloaded != 0 {
		t.Fatal("16 GB: a voice was freed though all fit")
	}
	reset("one")
	g.makeRoom(context.Background(), "qwen3")
	if fakes["chatterbox"].unloaded != 1 || fakes["omnivoice"].unloaded != 1 {
		t.Fatal("one: not every other GPU voice was freed")
	}
	reset("4") // qwen3 alone is 5.5: the others go, and it still loads
	g.makeRoom(context.Background(), "qwen3")
	if fakes["chatterbox"].unloaded != 1 || fakes["omnivoice"].unloaded != 1 {
		t.Fatal("4 GB: the others were not freed for a voice over the budget")
	}
	reset("off")
	g.makeRoom(context.Background(), "qwen3")
	if fakes["chatterbox"].unloaded+fakes["omnivoice"].unloaded != 0 {
		t.Fatal("no limit: a voice was freed")
	}
}

// The card's real free memory counts too, whatever the budget: another
// program (Higgs in WSL) holding the card makes voice free its own voices,
// least recently used first, and when that is not enough the voice does not
// load, with an error that says why.
func TestMakeRoomFreeMemory(t *testing.T) {
	old, oldMem := voiceEngine, gpuMemory
	defer func() { voiceEngine, gpuMemory = old, oldMem }()
	var fakes map[string]*fakeGPU
	voiceEngine = func(name string) (engine.Engine, error) {
		if f, ok := fakes[name]; ok {
			return f, nil
		}
		return &fakeGPU{}, nil
	}
	cfg := &preset.Config{}
	g := &gpuBudget{used: map[string]time.Time{}, measured: map[string]float64{}, config: func() *preset.Config { return cfg }}
	var others float64 // GB another program holds
	gpuMemory = func(context.Context) (float64, float64, bool) {
		used := others
		for name, f := range fakes {
			if f.loaded {
				used += mustBackend(t, name).VRAM
			}
		}
		return 16 - used, 16, true
	}
	reset := func(other float64) {
		others = other
		fakes = map[string]*fakeGPU{"chatterbox": {loaded: true}, "omnivoice": {loaded: true}}
		now := time.Now()
		g.used = map[string]time.Time{"chatterbox": now.Add(-time.Minute), "omnivoice": now}
	}

	reset(0) // 8 GB free, qwen3 needs 5.5: nothing goes
	if err := g.makeRoom(context.Background(), "qwen3"); err != nil || fakes["chatterbox"].unloaded+fakes["omnivoice"].unloaded != 0 {
		t.Fatalf("room enough: %v", err)
	}
	reset(5) // 3 GB free: the least recently used voice goes, then it fits
	if err := g.makeRoom(context.Background(), "qwen3"); err != nil || fakes["chatterbox"].unloaded != 1 || fakes["omnivoice"].unloaded != 0 {
		t.Fatalf("one voice freed: %v, chatterbox %d omnivoice %d", err, fakes["chatterbox"].unloaded, fakes["omnivoice"].unloaded)
	}
	reset(15.6) // Higgs holds the card: freeing ours is not enough
	err := g.makeRoom(context.Background(), "qwen3")
	if err == nil || !strings.Contains(err.Error(), "another program is using the graphics card") {
		t.Fatalf("no room: %v", err)
	}
	if fakes["chatterbox"].unloaded != 1 || fakes["omnivoice"].unloaded != 1 {
		t.Fatal("voice's own voices were not freed first")
	}
}

func TestParseNvidiaMemory(t *testing.T) {
	if f, tot, ok := parseNvidiaMemory("7423, 16303\r\n2048, 8192\n"); !ok || f < 7.2 || f > 7.3 || tot < 15.9 || tot > 16 {
		t.Fatalf("first card: %v %v %v", f, tot, ok)
	}
	for _, bad := range []string{"", "[N/A], [N/A]", "No devices were found"} {
		if _, _, ok := parseNvidiaMemory(bad); ok {
			t.Fatalf("%q read as memory", bad)
		}
	}
}

// A measured size wins over the estimate, per model size.
func TestMeasuredSize(t *testing.T) {
	g := &gpuBudget{used: map[string]time.Time{}, measured: map[string]float64{}}
	g.measure("qwen3", map[string]string{"model": "Qwen/Qwen3-TTS-12Hz-1.7B-Base"}, 6656)
	cfg := &preset.Config{}
	b := mustBackend(t, "qwen3")
	if gb, measured := g.size(b, cfg); !measured || gb != 6.5 {
		t.Fatalf("1.7B: %v %v", gb, measured)
	}
	cfg.Sidecar = map[string]map[string]string{"qwen3": {"model": "Qwen/Qwen3-TTS-12Hz-0.6B-Base"}}
	if gb, measured := g.size(b, cfg); measured || gb != 3.5 {
		t.Fatalf("0.6B is still the estimate: %v %v", gb, measured)
	}
}

func mustBackend(t *testing.T, name string) sidecar.Backend {
	t.Helper()
	b, ok := sidecar.Find(name)
	if !ok {
		t.Fatalf("no backend %s", name)
	}
	return b
}
