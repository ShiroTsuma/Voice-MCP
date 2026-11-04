// Package engine defines the contract every speech backend implements.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Request is one synthesis call. Text is already cleaned and is usually one
// sentence; the core splits longer utterances so playback can start early.
type Request struct {
	Text  string
	Lang  string // "pl", "en", ...
	Voice Voice
	Speed float64 // 1.0 = engine default
}

// Voice selects a voice. Engines that do not support mixing use the heaviest
// component; Ref is a reference clip for cloning engines, RefText its
// transcript. Engines that cannot clone must reject a Ref so the core falls
// back instead of speaking with an unrelated voice.
type Voice struct {
	Name    string
	Mix     map[string]float64
	Ref     string
	RefText string
	// Prompt is a voice prompt file engine PromptEngine made from Ref once;
	// that engine reads no recording, the others use Ref as before.
	Prompt       string
	PromptEngine string
	PromptModel  string // the model it was made with; another model uses Ref
}

// ErrNoCloning is returned by engines that cannot use a reference clip.
var ErrNoCloning = errors.New("engine cannot clone voices")

func (v Voice) IsZero() bool { return v.Name == "" && len(v.Mix) == 0 && v.Ref == "" }

// Primary returns the single voice name to use when mixing is unsupported.
func (v Voice) Primary() string {
	if v.Name != "" {
		return v.Name
	}
	best, weight := "", -1.0
	for name, w := range v.Mix {
		if w > weight || (w == weight && name < best) {
			best, weight = name, w
		}
	}
	return best
}

// Chunk is mono PCM audio.
type Chunk struct {
	PCM        []float32
	SampleRate int
}

type VoiceInfo struct {
	Name   string
	Lang   string
	Gender string
}

// Engine synthesizes speech. Synthesize streams chunks to sink; an engine
// without streaming sends a single chunk.
type Engine interface {
	Voices(ctx context.Context) ([]VoiceInfo, error)
	Synthesize(ctx context.Context, req Request, sink func(Chunk) error) error
	Close() error
}

// Config is the raw [engines.<name>] table. Each factory reads the keys it knows.
type Config map[string]any

func (c Config) String(key string) string {
	s, _ := c[key].(string)
	return s
}

func (c Config) Strings(key string) []string {
	raw, _ := c[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Dirs are the tool's data locations, set once at startup. Relative model
// paths in engine configs resolve against Models.
var Dirs struct {
	Models  string // downloaded TTS models
	Runtime string // native libraries such as sherpa-onnx
}

// MixesKey is the engine config key under which the core passes every voice
// mix that presets request from that engine, so engines that bake mixes at
// startup can prepare them.
const MixesKey = "_mixes"

type Factory func(name string, cfg Config) (Engine, error)

var (
	mu        sync.Mutex
	factories = map[string]Factory{}
)

// Register makes an engine type available to configuration.
func Register(typ string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	factories[typ] = f
}

func New(name string, cfg Config) (Engine, error) {
	typ := cfg.String("type")
	mu.Lock()
	f := factories[typ]
	mu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("engine %q: unknown type %q (known: %v)", name, typ, Types())
	}
	return f(name, cfg)
}

func Types() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(factories))
	for t := range factories {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
