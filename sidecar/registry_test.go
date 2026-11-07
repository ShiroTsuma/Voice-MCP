package sidecar

import (
	"slices"
	"testing"
)

func TestRegistry(t *testing.T) {
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	q, ok := Find("qwen3")
	if !ok || !q.Transcript || q.Option == nil {
		t.Fatalf("qwen3: %+v", q)
	}
	start := q.Engine(map[string]string{"model": "Qwen/Qwen3-TTS-12Hz-0.6B-Base"})["start"].([]any)
	if !slices.Contains(start, any("model=Qwen/Qwen3-TTS-12Hz-0.6B-Base")) || !slices.Contains(start, any("8768")) {
		t.Fatalf("start %v", start)
	}
	if def := q.Engine(nil)["start"].([]any); !slices.Contains(def, any("model="+q.Option.Default)) {
		t.Fatalf("default start %v", def)
	}
	if slices.Contains(Names(false), "test") || !slices.Contains(Names(true), "test") {
		t.Fatal("hidden backend")
	}
	// The fast Qwen3 shares qwen3's prompts, model choices, and needs NVIDIA;
	// it renders sentence by sentence (no batches).
	f, ok := Find("qwen3_fast")
	if !ok || PromptFormat("qwen3_fast") != "qwen3" || PromptFormat("qwen3") != "qwen3" || f.Batch || !f.RunsOn(map[string]bool{"nvidia": true}) || f.RunsOn(map[string]bool{"amd": true}) {
		t.Fatalf("qwen3_fast: %+v", f)
	}
	if f.Option == nil || f.Option.Key != q.Option.Key || f.Option.Default != q.Option.Default {
		t.Fatal("qwen3_fast must offer qwen3's model choices, or the prompts' model check fails")
	}
	if PromptFormat("chatterbox") != "" || PromptFormat("nope") != "" {
		t.Fatal("an engine without prompts has a prompt format")
	}
	// Chatterbox Nano runs on the processor on any PC, an AMD one or one
	// without a graphics card too, and takes no graphics memory.
	n, ok := Find("chatterbox_nano")
	if !ok || !n.CPU || n.VRAM != 0 || n.GPUNames() != "" || !n.RunsOn(map[string]bool{"intel": true}) || !n.RunsOn(map[string]bool{"amd": true}) || n.EnvName() != "chatterbox_nano" {
		t.Fatalf("chatterbox_nano: %+v", n)
	}
	// Turbo shares Chatterbox's environment, so the two get the same build.
	cb, _ := Find("chatterbox")
	turbo, _ := Find("chatterbox_turbo")
	if cb.SourceKey() == "" || turbo.SourceKey() != cb.SourceKey() || n.SourceKey() != cb.SourceKey() {
		t.Fatal("the Chatterbox backends must install the same chatterbox-tts build")
	}
}

func TestRunsOn(t *testing.T) {
	b := Backend{GPUs: []string{"nvidia", "amd"}}
	if b.GPUNames() != "an NVIDIA or AMD graphics card" {
		t.Fatal(b.GPUNames())
	}
	for _, c := range []struct {
		found map[string]bool
		want  bool
	}{
		{map[string]bool{"nvidia": true, "intel": true}, true},
		{map[string]bool{"amd": true}, true},
		{map[string]bool{"intel": true}, false},
		{nil, true}, // unknown: offered
	} {
		if got := b.RunsOn(c.found); got != c.want {
			t.Errorf("%v: %v", c.found, got)
		}
	}
	if !(Backend{}).RunsOn(map[string]bool{"intel": true}) {
		t.Error("a backend without gpus runs anywhere")
	}
	for _, b := range Backends() {
		for _, g := range b.GPUs {
			if g != "nvidia" && g != "amd" && g != "intel" {
				t.Errorf("%s: unknown gpu %q", b.Name, g)
			}
		}
	}
}
