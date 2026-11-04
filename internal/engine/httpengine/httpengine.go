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
	options map[string]string // --option key=value in start: what the server must run with
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
	e.options = startOptions(e.start)
	return e, nil
}

// chosen holds options set while voice runs (a model size picked in the
// Integrations window), by engine name; they win over the start command's.
var chosen sync.Map

// SetOptions sets an engine's options from now on: the next request finds
// the server running with others and starts it again with these.
func SetOptions(engine string, set map[string]string) {
	cp := make(map[string]string, len(set))
	for k, v := range set {
		cp[k] = v
	}
	chosen.Store(engine, cp)
}

// want is what the server must run with: the start command's options with
// the chosen ones over them.
func (e *Engine) want() map[string]string {
	out := make(map[string]string, len(e.options))
	for k, v := range e.options {
		out[k] = v
	}
	if set, ok := chosen.Load(e.name); ok {
		for k, v := range set.(map[string]string) {
			if _, known := out[k]; known {
				out[k] = v
			}
		}
	}
	return out
}

// startOptions reads the --option key=value pairs of a start command.
func startOptions(start []string) map[string]string {
	out := map[string]string{}
	for i := 0; i+1 < len(start); i++ {
		if start[i] == "--option" {
			if k, v, ok := strings.Cut(start[i+1], "="); ok {
				out[k] = v
			}
		}
	}
	return out
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
	up, _, stale := e.probe(ctx)
	return up && !stale
}

// probe asks the server for its health. up: the engine's server answered and
// is ready; other: something answered that is not it (another program holds
// the port), which starting the server again cannot fix; stale: it is the
// engine's server, started with other options (another model size), and
// must make way for one with these.
func (e *Engine) probe(ctx context.Context) (up, other, stale bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, false, false // nothing listens
	}
	defer resp.Body.Close()
	var h struct {
		Backend string            `json:"backend"`
		Ready   *bool             `json:"ready"`
		Options map[string]string `json:"options"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h) != nil {
		return false, true, false
	}
	// A server of voice's sidecar says its backend; any other server speaking
	// the protocol may say nothing, and counts as up.
	if h.Backend != "" && len(e.start) > 0 && !strings.Contains(strings.Join(e.start, " "), h.Backend) {
		return false, true, false
	}
	for k, v := range e.want() {
		if h.Options != nil && h.Options[k] != v {
			stale = true
		}
	}
	return h.Ready == nil || *h.Ready, false, stale
}

// shutdown asks a stale server to exit and waits until its port is free.
func (e *Engine) shutdown(ctx context.Context) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/shutdown", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	for i := 0; i < 50; i++ {
		if up, _, _ := e.probe(ctx); !up {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// OnStarting, when set, learns that voice waits for an engine's server to
// start and load its model (on true) and that the wait is over (false), so the
// settings window can say why the first sentence takes a while.
var OnStarting func(engine string, starting bool)

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
	up, other, stale := e.probe(ctx)
	if up && stale {
		e.shutdown(ctx)
		e.running = nil // it was started with the old options
		other = false
	}
	if other {
		return fmt.Errorf("engine %q: another program answers at %s, the port voice's %s server uses; close it, or give the engine another port in config.toml", e.name, e.url, e.name)
	}
	if fn := OnStarting; fn != nil {
		fn(e.name, true)
		defer fn(e.name, false)
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
		want := e.want()
		args := make([]string, len(e.start))
		for i, a := range e.start {
			if i > 0 && e.start[i-1] == "--option" {
				if k, _, ok := strings.Cut(a, "="); ok {
					a = k + "=" + want[k]
				}
			}
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

// PromptInfo is what a saved voice prompt was made with.
type PromptInfo struct {
	Model    string `json:"model"`
	Revision string `json:"revision"`
}

// MakePrompt has the server make a voice prompt from a recording and its
// transcript and save it to out (see the sidecar's POST /prompt).
func (e *Engine) MakePrompt(ctx context.Context, ref, text, out string) (PromptInfo, error) {
	if err := e.ensure(ctx); err != nil {
		return PromptInfo{}, err
	}
	body, _ := json.Marshal(map[string]string{"ref_audio": ref, "ref_text": text, "out": out})
	hr, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/prompt", bytes.NewReader(body))
	hr.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(hr)
	if err != nil {
		return PromptInfo{}, fmt.Errorf("engine %q: %w", e.name, err)
	}
	defer resp.Body.Close()
	var out2 struct {
		PromptInfo
		Error string `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out2)
	if resp.StatusCode != http.StatusOK {
		if out2.Error == "" {
			out2.Error = resp.Status
		}
		return PromptInfo{}, fmt.Errorf("engine %q: %s", e.name, out2.Error)
	}
	return out2.PromptInfo, nil
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
	payload := map[string]any{
		"text": req.Text, "lang": req.Lang, "voice": req.Voice.Primary(),
		"ref_audio": req.Voice.Ref, "ref_text": req.Voice.RefText, "speed": req.Speed, "extra": e.extra,
	}
	// A voice prompt this engine made, with the model it runs now: it reads
	// that, not the recording. With another model size it uses the recording.
	if v := req.Voice; v.Prompt != "" && v.PromptEngine == e.name {
		if m := e.want()["model"]; v.PromptModel == "" || m == "" || m == v.PromptModel {
			payload["voice_prompt"] = v.Prompt
		}
	}
	body, _ := json.Marshal(payload)
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
