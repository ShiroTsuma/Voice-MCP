package httpengine

import (
	"context"
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
	if up, other := e.probe(context.Background()); up || !other {
		t.Fatalf("probe up=%v other=%v", up, other)
	}
}

func TestOwnServerIsUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"backend":"chatterbox","ready":true}`))
	}))
	defer srv.Close()
	e := &Engine{name: "chatterbox", url: srv.URL, start: []string{"python", "--backend", "chatterbox"}}
	if up, other := e.probe(context.Background()); !up || other {
		t.Fatalf("probe up=%v other=%v", up, other)
	}
	e.start = []string{"python", "--backend", "xtts"} // another backend answers there
	if up, other := e.probe(context.Background()); up || !other {
		t.Fatalf("other backend: up=%v other=%v", up, other)
	}
}
