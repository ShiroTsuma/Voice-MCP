package core

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"voice/internal/engine"
	"voice/internal/preset"
)

// turnEngine says each sentence as a short tone and keeps what it was asked.
type turnEngine struct {
	mu   sync.Mutex
	said []string
}

var turnEng = &turnEngine{}

func (e *turnEngine) Voices(context.Context) ([]engine.VoiceInfo, error) { return nil, nil }
func (e *turnEngine) Close() error                                       { return nil }
func (e *turnEngine) Synthesize(_ context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	e.mu.Lock()
	e.said = append(e.said, req.Text)
	e.mu.Unlock()
	pcm := make([]float32, 2400) // 0.1 s of tone, then 0.5 s of padding to trim
	for i := range pcm {
		pcm[i] = float32(0.4 * math.Sin(float64(i)/3))
	}
	return sink(engine.Chunk{PCM: append(pcm, make([]float32, 12000)...), SampleRate: 24000})
}
func (e *turnEngine) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.said
	e.said = nil
	return out
}

func init() {
	engine.Register("turntest", func(string, engine.Config) (engine.Engine, error) { return turnEng, nil })
}

func turnSpeaker(t *testing.T) *Speaker {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(conf, []byte("default_preset = \"tt\"\n[engines.turntest]\ntype = \"turntest\"\n[presets.tt]\nengine = \"turntest\"\nchime = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := preset.Load(conf)
	if err != nil {
		t.Fatal(err)
	}
	sp := NewSpeaker(cfg)
	sp.RecordTo(filepath.Join(dir, "out.wav"))
	t.Cleanup(sp.Close)
	turnEng.take()
	return sp
}

func done(t *testing.T, tk Ticket) Result {
	t.Helper()
	select {
	case r := <-tk.Done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never ended", tk.ID)
	}
	return Result{}
}

const (
	s1 = "The first sentence of the reply is here."
	s2 = "The second sentence follows right after."
	s3 = "A third sentence arrives as its own piece."
)

// Pieces of one reply with the same turn are one utterance, said in order,
// before anything queued after them; another session never joins.
func TestTurnJoins(t *testing.T) {
	sp := turnSpeaker(t)
	a, err := sp.Speak(Request{Text: s1 + " " + s2, Lang: "en", Owner: 5, Turn: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := sp.Speak(Request{Text: "Another session speaks now, after the reply.", Lang: "en", Owner: 6, Turn: "r1"})
	b, err := sp.Speak(Request{Text: s3, Lang: "en", Owner: 5, Turn: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Appended || b.ID != a.ID || b.Done != nil {
		t.Fatalf("second piece %+v, want appended to %s", b, a.ID)
	}
	if other.Appended || other.ID == a.ID {
		t.Fatalf("another owner joined the reply: %+v", other)
	}
	sp.EndTurn(5)
	if r := done(t, a); r.Err != nil {
		t.Fatal(r.Err)
	}
	done(t, other)
	if got := turnEng.take(); !slices.Equal(got, []string{s1, s2, s3, "Another session speaks now, after the reply."}) {
		t.Fatalf("said %q", got)
	}
	tr, _ := sp.Track(a.ID)
	if tr.State != "done" {
		t.Fatalf("track %+v", tr)
	}

	// Once said, the same turn starts a new utterance.
	c, _ := sp.Speak(Request{Text: s1, Lang: "en", Owner: 5, Turn: "r1"})
	if c.Appended || c.ID == a.ID {
		t.Fatalf("joined an utterance already said: %+v", c)
	}
	sp.EndTurn(5)
	done(t, c)
}

// Speech without a turn, or with another, closes the open one; a turn left
// open ends by itself once all of it was heard.
func TestTurnCloses(t *testing.T) {
	old := turnIdle
	turnIdle = 200 * time.Millisecond
	t.Cleanup(func() { turnIdle = old })
	sp := turnSpeaker(t)

	a, _ := sp.Speak(Request{Text: s1, Lang: "en", Owner: 5, Turn: "r1"})
	b, _ := sp.Speak(Request{Text: s2, Lang: "en", Owner: 5})
	if b.Appended {
		t.Fatal("speech without a turn joined one")
	}
	c, _ := sp.Speak(Request{Text: s3, Lang: "en", Owner: 5, Turn: "r1"})
	if c.Appended {
		t.Fatal("joined a turn that speech without one had closed")
	}
	d, _ := sp.Speak(Request{Text: s1, Lang: "en", Owner: 5, Turn: "r2"})
	if d.Appended {
		t.Fatal("another turn joined")
	}
	for _, tk := range []Ticket{a, b, c} {
		done(t, tk)
	}
	start := time.Now()
	done(t, d) // left open: ends turnIdle after it was heard
	if time.Since(start) > 5*time.Second {
		t.Fatal("an open turn did not end by itself")
	}

	// A stop ends an open turn at once.
	e, _ := sp.Speak(Request{Text: s1, Lang: "en", Owner: 7, Turn: "r9"})
	time.Sleep(50 * time.Millisecond)
	sp.StopOwner(7)
	if r := done(t, e); r.Err == nil {
		t.Fatal("a stopped turn ended as done")
	}
}
