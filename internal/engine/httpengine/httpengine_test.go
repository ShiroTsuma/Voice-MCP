package httpengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"voice/internal/audio"
	"voice/internal/engine"
)

func TestProtocol(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"ready":true}`))
		case "/synthesize":
			json.NewDecoder(r.Body).Decode(&got)
			if got["ref_audio"] == "nope.wav" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"backend x cannot clone voices"}`))
				return
			}
			w.Header().Set("Content-Type", "audio/wav")
			w.Write(audio.EncodeWAV([]float32{0.1, 0.2, 0.3}, 22050))
		}
	}))
	defer srv.Close()

	e, err := New("t", engine.Config{"url": srv.URL, "extra": map[string]any{"cfg_weight": 0.3}})
	if err != nil {
		t.Fatal(err)
	}
	var chunk engine.Chunk
	req := engine.Request{Text: "hi", Lang: "en", Speed: 0.9, Voice: engine.Voice{Ref: "me.wav", RefText: "hello"}}
	if err := e.Synthesize(context.Background(), req, func(c engine.Chunk) error { chunk = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if chunk.SampleRate != 22050 || len(chunk.PCM) != 3 {
		t.Errorf("chunk %+v", chunk)
	}
	if got["ref_audio"] != "me.wav" || got["ref_text"] != "hello" || got["extra"].(map[string]any)["cfg_weight"] != 0.3 {
		t.Errorf("request %v", got)
	}
	req.Voice.Ref = "nope.wav"
	if err := e.Synthesize(context.Background(), req, func(engine.Chunk) error { return nil }); !errors.Is(err, engine.ErrNoCloning) {
		t.Errorf("clone refusal: %v", err)
	}
}

func TestNoServerNoStart(t *testing.T) {
	e, _ := New("t", engine.Config{"url": "http://127.0.0.1:1"})
	if err := e.Synthesize(context.Background(), engine.Request{Text: "x"}, func(engine.Chunk) error { return nil }); err == nil {
		t.Fatal("expected error")
	}
}

func TestExpandPrefersTheBackendsOwnEnvironment(t *testing.T) {
	old := SidecarDir
	defer func() { SidecarDir = old }()
	SidecarDir = t.TempDir()
	shared := filepath.Join(SidecarDir, ".venv", "Scripts", "python.exe")
	if got := expand("{python:chatterbox}"); got != shared {
		t.Fatalf("without envs: %s", got)
	}
	own := filepath.Join(SidecarDir, "envs", "chatterbox", "Scripts", "python.exe")
	os.MkdirAll(filepath.Dir(own), 0o755)
	os.WriteFile(own, nil, 0o644)
	if got := expand("{python:chatterbox}"); got != own {
		t.Fatalf("with envs: %s", got)
	}
	if got := expand(`{sidecar}\x`); got != SidecarDir+`\x` {
		t.Fatalf("sidecar: %s", got)
	}
}

func TestAnotherProgramOnThePort(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler()) // like a dev web server on the same port
	defer srv.Close()
	e := &Engine{name: "chatterbox", url: srv.URL, start: []string{"python", "--backend", "chatterbox"}, timeout: time.Minute}
	began := time.Now()
	err := e.ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another program") {
		t.Fatalf("got %v", err)
	}
	if time.Since(began) > 5*time.Second {
		t.Fatal("it waited instead of saying so")
	}
	if up, other, _ := e.probe(context.Background()); up || !other {
		t.Fatalf("probe up=%v other=%v", up, other)
	}
}

func TestOwnServerIsUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"backend":"chatterbox","ready":true}`))
	}))
	defer srv.Close()
	e := &Engine{name: "chatterbox", url: srv.URL, start: []string{"python", "--backend", "chatterbox"}}
	if up, other, _ := e.probe(context.Background()); !up || other {
		t.Fatalf("probe up=%v other=%v", up, other)
	}
	e.start = []string{"python", "--backend", "xtts"} // another backend answers there
	if up, other, _ := e.probe(context.Background()); up || !other {
		t.Fatalf("other backend: up=%v other=%v", up, other)
	}
}

func TestServerWithOtherOptionsMakesWay(t *testing.T) {
	model, down := "Qwen/0.6B", false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if down {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"backend": "qwen3", "ready": true, "options": map[string]string{"model": model}})
		case "/shutdown":
			down = true
		}
	}))
	defer srv.Close()
	start := []any{"python", "-m", "voice_sidecar", "--backend", "qwen3", "--option", "model=Qwen/1.7B"}
	ei, err := New("qwen3", engine.Config{"url": srv.URL, "start": start})
	if err != nil {
		t.Fatal(err)
	}
	e := ei.(*Engine)
	if up, other, stale := e.probe(context.Background()); !up || other || !stale {
		t.Fatalf("other model: up %v other %v stale %v", up, other, stale)
	}
	if e.healthy(context.Background()) {
		t.Fatal("a server with the old model counts as healthy")
	}
	e.shutdown(context.Background())
	if !down {
		t.Fatal("shutdown not asked")
	}
	model, down = "Qwen/1.7B", false
	if !e.healthy(context.Background()) {
		t.Fatal("the server with the chosen model is not healthy")
	}
}

func TestVoicePromptGoesToItsEngineAndModel(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/synthesize" {
			got = nil
			json.NewDecoder(r.Body).Decode(&got)
			w.Write(audio.EncodeWAV([]float32{0.1, 0.2}, 24000))
			return
		}
		w.Write([]byte(`{"ready":true}`))
	}))
	defer srv.Close()
	send := func(name, model string, v engine.Voice) bool {
		ei, _ := New(name, engine.Config{"url": srv.URL, "start": []any{"x", "--option", "model=" + model}})
		e := ei.(*Engine)
		e.start = nil // the fake server is up; nothing to start
		if err := e.Synthesize(context.Background(), engine.Request{Text: "hi", Voice: v}, func(engine.Chunk) error { return nil }); err != nil {
			t.Fatal(err)
		}
		_, ok := got["voice_prompt"]
		return ok
	}
	v := engine.Voice{Ref: "ref.wav", Prompt: "prompt.pt", PromptEngine: "qwen3", PromptModel: "big"}
	if !send("qwen3", "big", v) {
		t.Fatal("the prompt did not go to its engine")
	}
	if send("qwen3", "small", v) {
		t.Fatal("a prompt went to another model size")
	}
	if send("chatterbox", "big", v) {
		t.Fatal("a qwen3 prompt went to chatterbox")
	}
}

func TestBatchOfSentences(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/synthesize_batch" {
			w.Write([]byte(`{"ready":true}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		var audioOut []string
		for i := range got["texts"].([]any) {
			audioOut = append(audioOut, base64.StdEncoding.EncodeToString(audio.EncodeWAV(make([]float32, 100*(i+1)), 24000)))
		}
		json.NewEncoder(w).Encode(map[string]any{"audio": audioOut})
	}))
	defer srv.Close()
	ei, _ := New("qwen3", engine.Config{"url": srv.URL, "batch": true})
	chunks, err := ei.(engine.Batcher).SynthesizeBatch(context.Background(), []engine.Request{{Text: "One."}, {Text: "Two."}, {Text: "Three."}})
	if err != nil || len(chunks) != 3 || len(chunks[2].PCM) != 300 || chunks[0].SampleRate != 24000 {
		t.Fatalf("batch: %d chunks, %v", len(chunks), err)
	}
	if texts := got["texts"].([]any); len(texts) != 3 || texts[1] != "Two." || got["text"] != nil {
		t.Fatalf("sent %v", got)
	}
	plain, _ := New("chatterbox", engine.Config{"url": srv.URL})
	if _, err := plain.(engine.Batcher).SynthesizeBatch(context.Background(), []engine.Request{{Text: "x"}, {Text: "y"}}); err == nil {
		t.Fatal("an engine without batches took one")
	}
}

func TestIdleReachesRunningServers(t *testing.T) {
	got := make(chan float64, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/idle" {
			var b struct{ Seconds float64 }
			json.NewDecoder(r.Body).Decode(&b)
			got <- b.Seconds
		}
		w.Write([]byte(`{"ready":true}`))
	}))
	defer srv.Close()
	New("managed", engine.Config{"url": srv.URL, "start": []any{"python", "--idle-seconds", "600"}})
	New("external", engine.Config{"url": srv.URL}) // a server voice does not run: left alone
	SetIdle(3600)
	select {
	case s := <-got:
		if s != 3600 {
			t.Fatalf("idle %v", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the running server was not told")
	}
	select {
	case <-got:
		t.Fatal("a server voice does not run was told")
	case <-time.After(300 * time.Millisecond):
	}
	if idleSeconds.Load() != 3600 {
		t.Fatal("not kept for new servers")
	}
	SetIdle(-1)
}
