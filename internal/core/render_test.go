package core

import (
	"reflect"
	"testing"

	"voice/internal/engine"
	"voice/internal/preset"
)

func TestEngineGroups(t *testing.T) {
	got := engineGroups([]string{"qwen3", "kokoro", "higgs", "qwen3", "higgs", "kokoro"})
	want := []engineGroup{{"qwen3", []int{0, 3}}, {"kokoro", []int{1, 5}}, {"higgs", []int{2, 4}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

// A script across two GPU engines renders first; one GPU engine with CPU
// voices plays as it renders; render, play and a file decide by themselves.
func TestRenderFirst(t *testing.T) {
	cfg := &preset.Config{DefaultLang: "en", Engines: map[string]engine.Config{
		"qwen3": {"type": "http"}, "higgs": {"type": "openai"}, "kokoro": {"type": "sherpa"},
	}}
	s := &Speaker{cfg: cfg}
	on := func(e string) part {
		return part{preset: preset.Resolved{Name: e, Engine: e}}
	}
	gpu2 := []part{on("qwen3"), on("higgs"), on("qwen3")}
	gpu1 := []part{on("qwen3"), on("kokoro")}
	for _, c := range []struct {
		mode, save string
		parts      []part
		want       bool
	}{
		{"", "", gpu2, true}, {"auto", "", gpu1, false}, {"play", "", gpu2, false},
		{"render", "", gpu1, true}, {"play", "x.wav", gpu1, true},
	} {
		if got := s.renderFirst(c.mode, c.save, c.parts); got != c.want {
			t.Errorf("%q %q %d parts: %v", c.mode, c.save, len(c.parts), got)
		}
	}
}
