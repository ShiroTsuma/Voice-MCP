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
