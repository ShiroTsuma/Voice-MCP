package core

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"voice/internal/engine"
	"voice/internal/preset"
)

// flowEngine records the requests it gets.
type flowEngine struct {
	mu   sync.Mutex
	reqs []engine.Request
}

var flowEng = &flowEngine{}

func (e *flowEngine) Voices(context.Context) ([]engine.VoiceInfo, error) { return nil, nil }
func (e *flowEngine) Close() error                                       { return nil }
func (e *flowEngine) Synthesize(_ context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()
	pcm := make([]float32, 2400)
	for i := range pcm {
		pcm[i] = float32(0.4 * math.Sin(float64(i)/3))
	}
	return sink(engine.Chunk{PCM: pcm, SampleRate: 24000})
}

func init() {
	engine.Register("flowtest", func(string, engine.Config) (engine.Engine, error) { return flowEng, nil })
}

// A voice set to flow hands each sentence the one just said; the first has
// none, and a voice without flow gets none at all.
func TestFlowPassesTheSentenceBefore(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "config.toml")
	os.WriteFile(conf, []byte("default_preset = \"flowing\"\n[engines.flowtest]\ntype = \"flowtest\"\n"+
		"[presets.flowing]\nengine = \"flowtest\"\nchime = \"none\"\nflow = \"clip\"\n"+
		"[presets.plain]\nengine = \"flowtest\"\nchime = \"none\"\n"+
		"[presets.odd]\nengine = \"flowtest\"\nchime = \"none\"\nflow = \"sideways\"\n"), 0o644)
	cfg, err := preset.Load(conf)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := cfg.Resolve("odd"); r.Flow != "" {
		t.Fatalf("an unknown flow mode was taken: %q", r.Flow)
	}
	sp := NewSpeaker(cfg)
	sp.RecordTo(filepath.Join(dir, "out.wav"))
	t.Cleanup(sp.Close)

	say := func(p string) []engine.Request {
		flowEng.mu.Lock()
		flowEng.reqs = nil
		flowEng.mu.Unlock()
		tk, err := sp.Speak(Request{Text: "The first sentence of this reply is long enough to stand alone. Then comes a second sentence, just as long as the one before it. And a third one closes the reply with a little more to say.", Preset: p})
		if err != nil {
			t.Fatal(err)
		}
		done(t, tk)
		flowEng.mu.Lock()
		defer flowEng.mu.Unlock()
		return append([]engine.Request(nil), flowEng.reqs...)
	}

	reqs := say("flowing")
	if len(reqs) != 3 {
		t.Fatalf("%d requests", len(reqs))
	}
	if reqs[0].Prev != nil || reqs[0].Flow != engine.FlowClip {
		t.Fatalf("first sentence: prev %v flow %q", reqs[0].Prev, reqs[0].Flow)
	}
	for i := 1; i < 3; i++ {
		p := reqs[i].Prev
		if p == nil || p.Text != reqs[i-1].Text || len(p.PCM) == 0 || p.Rate <= 0 {
			t.Fatalf("sentence %d did not get the one before: %+v", i, p)
		}
	}

	for _, r := range say("plain") {
		if r.Prev != nil || r.Flow != "" {
			t.Fatal("a voice without flow was given the sentence before")
		}
	}
}
