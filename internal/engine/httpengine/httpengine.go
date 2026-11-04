// Package httpengine talks to a speech server over voice's sidecar protocol
// (see sidecar/voice_sidecar). It can start the server on first use.
//
//	[engines.chatterbox]
//	type = "http"
//	url = "http://127.0.0.1:8765"
//	start = ["{python:chatterbox}", "-m", "voice_sidecar", "--backend", "chatterbox", "--port", "8765"]
//	start_timeout = 300      # seconds to wait for model load
//	extra = { exaggeration = 0.4, cfg_weight = 0.3 }
//
// {sidecar} in start expands to the sidecar directory, {python:NAME} to the
// Python of that backend's environment (see expand).
package httpengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/engine"
)

func init() { engine.Register("http", New) }

// Set at startup. SidecarDir holds the Python environment and model weights
// and is substituted for {sidecar} in start commands; SidecarCode is the
// voice_sidecar package shipped next to voice.exe.
var SidecarDir, SidecarCode string

type Engine struct {
	name    string
	url     string
	start   []string
	timeout time.Duration
	extra   map[string]any
	client  *http.Client

	mu      sync.Mutex
	running chan struct{} // open while a server this engine started is alive
}

func New(name string, cfg engine.Config) (engine.Engine, error) {
	url := strings.TrimRight(cfg.String("url"), "/")
	if url == "" {
		return nil, fmt.Errorf("engine %q: http needs url", name)
	}
	e := &Engine{name: name, url: url, start: cfg.Strings("start"), timeout: 300 * time.Second, client: &http.Client{Timeout: 5 * time.Minute}}
	if t, ok := cfg["start_timeout"].(int64); ok && t > 0 {
		e.timeout = time.Duration(t) * time.Second
	}
	e.extra, _ = cfg["extra"].(map[string]any)
	return e, nil
}

func (e *Engine) Close() error { return nil }

var pythonOf = regexp.MustCompile(`\{python:([A-Za-z0-9_-]+)\}`)

// expand fills a start argument: {sidecar} is the sidecar dir, {python:NAME}
// the python.exe of backend NAME's own environment (envs\NAME), or of the one
// shared environment (.venv) that setups before per-backend environments made.
func expand(a string) string {
	a = pythonOf.ReplaceAllStringFunc(a, func(m string) string {
		name := pythonOf.FindStringSubmatch(m)[1]
		own := filepath.Join(SidecarDir, "envs", name, "Scripts", "python.exe")
		if _, err := os.Stat(own); err == nil {
			return own
		}
		return filepath.Join(SidecarDir, ".venv", "Scripts", "python.exe")
	})
	return strings.ReplaceAll(a, "{sidecar}", SidecarDir)
}

func (e *Engine) healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ensure starts the server when it is down and a start command is set. The
// server keeps running after voice exits, so later processes reuse it.
func (e *Engine) ensure(ctx context.Context) error {
	if e.healthy(ctx) {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.healthy(ctx) {
		return nil
	}
	if len(e.start) == 0 {
		return fmt.Errorf("engine %q: no server at %s", e.name, e.url)
	}
	// A server started earlier may have exited (it stops itself when idle);
	// start a new one unless the last one is still loading.
	alive := false
	if e.running != nil {
		select {
		case <-e.running:
		default:
			alive = true
		}
	}
	if !alive {
		args := make([]string, len(e.start))
		for i, a := range e.start {
			args[i] = expand(a)
		}
		cmd := exec.Command(args[0], args[1:]...)
		os.MkdirAll(SidecarDir, 0o755)
		cmd.Dir = SidecarDir
		cmd.Env = append(os.Environ(), "PYTHONPATH="+SidecarCode)
		if os.Getenv("HF_HOME") == "" {
			// Model weights follow sidecar_dir instead of the user profile.
			cmd.Env = append(cmd.Env, "HF_HOME="+filepath.Join(SidecarDir, "models"))
		}
		if log, err := os.Create(filepath.Join(SidecarDir, e.name+".log")); err == nil {
			cmd.Stdout, cmd.Stderr = log, log
		}
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("engine %q: start server: %w", e.name, err)
		}
		running := make(chan struct{})
		e.running = running
		go func() { cmd.Wait(); close(running) }()
	}
	deadline := time.Now().Add(e.timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if e.healthy(ctx) {
			return nil
		}
	}
	return fmt.Errorf("engine %q: server did not come up within %s; see %s", e.name, e.timeout, filepath.Join(SidecarDir, e.name+".log"))
}

func (e *Engine) Voices(ctx context.Context) ([]engine.VoiceInfo, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/voices", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []engine.VoiceInfo
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

func (e *Engine) Synthesize(ctx context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	if err := e.ensure(ctx); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"text": req.Text, "lang": req.Lang, "voice": req.Voice.Primary(),
		"ref_audio": req.Voice.Ref, "ref_text": req.Voice.RefText, "speed": req.Speed, "extra": e.extra,
	})
	hr, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/synthesize", bytes.NewReader(body))
	hr.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(hr)
	if err != nil {
		return fmt.Errorf("engine %q: %w", e.name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var msg struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &msg) == nil && strings.Contains(msg.Error, "cannot clone") {
			return fmt.Errorf("engine %q: %w", e.name, engine.ErrNoCloning)
		}
		if msg.Error == "" {
			msg.Error = resp.Status
		}
		return fmt.Errorf("engine %q: %s", e.name, msg.Error)
	}
	pcm, rate, err := audio.DecodeWAV(data)
	if err != nil {
		return fmt.Errorf("engine %q: %w", e.name, err)
	}
	if len(pcm) == 0 {
		return errors.New("engine " + e.name + ": empty audio")
	}
	return sink(engine.Chunk{PCM: pcm, SampleRate: rate})
}
