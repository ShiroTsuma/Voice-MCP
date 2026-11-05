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
