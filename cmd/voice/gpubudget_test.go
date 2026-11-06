package main

import (
	"context"
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
	old := voiceEngine
	defer func() { voiceEngine = old }()
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
