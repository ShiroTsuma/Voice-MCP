package openaispeech

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/audio"
	"voice/internal/engine"
)

func TestSpeechServer(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[]}`))
		case "/v1/audio/speech":
			if r.Header.Get("Authorization") != "Bearer k" {
				http.Error(w, "no key", http.StatusUnauthorized)
				return
			}
			json.NewDecoder(r.Body).Decode(&got)
			w.Write(audio.EncodeWAV(make([]float32, 2400), 24000))
		}
	}))
	defer srv.Close()
	if err := Probe(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	e, err := New("higgs", engine.Config{"url": srv.URL + "/", "model": "m", "api_key": "k", "params": map[string]any{"temperature": 0.8}})
	if err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(t.TempDir(), "ref.wav")
	os.WriteFile(ref, audio.EncodeWAV(make([]float32, 100), 24000), 0o644)
	var rate int
	err = e.Synthesize(context.Background(), engine.Request{Text: "<|emotion:anger|> Hi", Voice: engine.Voice{Ref: ref, RefText: "hello"}, Extra: map[string]any{"top_k": 50}},
		func(c engine.Chunk) error { rate = c.SampleRate; return nil })
	if err != nil || rate != 24000 {
		t.Fatalf("synthesize: %v %d", err, rate)
	}
	refs, _ := got["references"].([]any)
	first, _ := refs[0].(map[string]any)
	if got["input"] != "<|emotion:anger|> Hi" || got["model"] != "m" || got["temperature"] != 0.8 || got["top_k"] != float64(50) || got["response_format"] != "wav" ||
		!strings.HasPrefix(first["audio_path"].(string), "data:audio/wav;base64,") || first["text"] != "hello" {
		t.Fatalf("body: %v", got)
	}
	// A server that cannot clone refuses a cloned voice, so another engine speaks.
	n, _ := New("plain", engine.Config{"url": srv.URL, "clone": "none"})
	if err := n.Synthesize(context.Background(), engine.Request{Text: "x", Voice: engine.Voice{Ref: ref}}, func(engine.Chunk) error { return nil }); err == nil {
		t.Fatal("a cloned voice was taken by an engine that cannot clone")
	}
}
