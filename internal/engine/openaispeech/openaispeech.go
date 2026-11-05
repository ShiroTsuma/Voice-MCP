// Package openaispeech speaks through a server with the OpenAI speech API
// (POST /v1/audio/speech), such as SGLang-Omni or vLLM-Omni serving Higgs
// Audio, or Kokoro-FastAPI. voice does not start the server; it runs
// somewhere voice can reach (WSL, Docker, another machine).
//
//	[engines.higgs]
//	type   = "openai"
//	url    = "http://127.0.0.1:8000"
//	model  = "bosonai/higgs-audio-v3-tts-4b"
//	voice  = "default"          # the server's voice when no clone is used
//	clone  = "references"       # how a cloned voice is sent: references, ref, or none
//	params = { temperature = 0.8, top_p = 0.8, top_k = 30 }   # sent with every request
//	api_key = ""                # sent as a bearer token, to this server only
//	speed  = false              # send "speed" (servers that do not take it refuse the request)
//
// A cloned voice's recording goes in the request as a data URL, so the
// server needs no access to voice's files (a server in WSL or Docker has
// none).
package openaispeech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/engine"
)

func init() { engine.Register("openai", New) }

type Engine struct {
	name, url, model, voice, clone, key string
	speed                               bool
	params                              map[string]any
	client                              *http.Client

	mu    sync.Mutex
	refs  map[string]string // recording path -> its data URL, while the file stays the same
	refAt map[string]time.Time
}

func New(name string, cfg engine.Config) (engine.Engine, error) {
	url := strings.TrimRight(cfg.String("url"), "/")
	if url == "" {
		return nil, fmt.Errorf("engine %q: openai needs url", name)
	}
	e := &Engine{name: name, url: url, model: cfg.String("model"), voice: cfg.String("voice"), clone: cfg.String("clone"),
		key: cfg.String("api_key"), client: &http.Client{Timeout: 5 * time.Minute}, refs: map[string]string{}, refAt: map[string]time.Time{}}
	if e.voice == "" {
		e.voice = "default"
	}
	if e.clone == "" {
		e.clone = "references"
	}
	e.speed, _ = cfg["speed"].(bool)
	e.params, _ = cfg["params"].(map[string]any)
	return e, nil
}

// Probe reports whether a speech server answers at url (its model list).
func Probe(ctx context.Context, url string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/v1/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}

func (e *Engine) Voices(context.Context) ([]engine.VoiceInfo, error) { return nil, nil }

func (e *Engine) Close() error { return nil }

// body is the request as the speech API has it.
func (e *Engine) body(req engine.Request) (map[string]any, error) {
	b := map[string]any{"input": req.Text, "voice": e.voice, "response_format": "wav"}
	if e.model != "" {
		b["model"] = e.model
	}
	maps.Copy(b, e.params)
	maps.Copy(b, req.Extra)
	if e.speed && req.Speed > 0 && req.Speed != 1 {
		b["speed"] = req.Speed
	}
	if v := req.Voice; v.Ref != "" {
		ref, err := e.dataURL(v.Ref)
		if err != nil {
			return nil, err
		}
		switch e.clone {
		case "references":
			b["references"] = []map[string]string{{"audio_path": ref, "text": v.RefText}}
		case "ref":
			b["ref_audio"], b["ref_text"] = ref, v.RefText
		default:
			// It cannot clone: refuse, so another engine speaks instead of a
			// voice the user did not choose.
			return nil, fmt.Errorf("engine %s does not clone voices", e.name)
		}
	} else if v.Name != "" && !strings.Contains(v.Name, ":") {
		b["voice"] = v.Name
	}
	return b, nil
}

// dataURL is a recording as a data URL, read again only when it changed.
func (e *Engine) dataURL(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if u, ok := e.refs[path]; ok && e.refAt[path].Equal(fi.ModTime()) {
		return u, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	u := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(b)
	e.refs[path], e.refAt[path] = u, fi.ModTime()
	return u, nil
}

func (e *Engine) Synthesize(ctx context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	body, err := e.body(req)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(body)
	hr, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/v1/audio/speech", bytes.NewReader(raw))
	hr.Header.Set("Content-Type", "application/json")
	if e.key != "" {
		hr.Header.Set("Authorization", "Bearer "+e.key)
	}
	resp, err := e.client.Do(hr)
	if err != nil {
		return fmt.Errorf("%w (is its speech server running at %s?)", err, e.url)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	pcm, rate, err := audio.DecodeWAV(data)
	if err != nil {
		return fmt.Errorf("the server's answer is not WAV audio: %w", err)
	}
	if len(pcm) == 0 {
		return errors.New("no audio")
	}
	return sink(engine.Chunk{PCM: pcm, SampleRate: rate})
}
