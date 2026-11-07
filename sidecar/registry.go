// Package sidecar is the list of GPU speech backends voice runs in its Python
// sidecar, read from backends.json: the same file the sidecar (Python) and
// setup.ps1 read, so a new backend is one entry there plus its Python module
// (see docs/adding-an-engine.md). voice makes an engine of each one.
package sidecar

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

//go:embed backends.json
var raw []byte

// Choice is one value of a backend's option, such as a model size.
type Choice struct {
	Value string  `json:"value"`
	Label string  `json:"label"`
	Note  string  `json:"note"`
	VRAM  float64 `json:"vram"` // graphics memory it takes, GB (an estimate); 0: the backend's
}

// Option is a setting of a backend the windows offer as a choice; the server
// gets it as --option key=value.
type Option struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Default string   `json:"default"`
	Choices []Choice `json:"choices"`
}

type Backend struct {
	Name  string   `json:"name"`
	Label string   `json:"label"`
	Note  string   `json:"note"`
	Pip   []string `json:"pip"`
	// Source are packages setup installs after Pip without their
	// dependencies: a newer build of one than its PyPI release (chatterbox-tts
	// from its repository). The environment records what it got, and a
	// changed Source makes setup install them again and the window offer it.
	Source     []string `json:"source"`
	Module     string   `json:"module"` // the import that proves it is installed
	Port       int      `json:"port"`
	Clones     bool     `json:"clones"`
	Transcript bool     `json:"transcript"` // cloning needs the clip's transcript
	Prompts    bool     `json:"prompts"`    // saves a voice prompt, so a clone speaks without its recording
	// PromptsAs names the backend whose voice prompt files it reads and
	// writes (the same model run another way); empty is its own.
	PromptsAs string `json:"prompts_as"`
	Batch     bool   `json:"batch"` // renders several sentences in one call (POST /synthesize_batch)
	// Flow: speaks a sentence from the one just said (/synthesize's flow,
	// prev_audio, prev_text), so a voice can be set to carry its mood on.
	Flow      bool           `json:"flow"`
	Languages []string       `json:"languages"` // what it speaks: en, pl, ...
	Hidden    bool           `json:"hidden"`    // not offered in the windows (test)
	Extra     map[string]any `json:"extra"`     // per-request settings voice sends
	Option    *Option        `json:"option"`
	// StartTimeout is how long voice waits for the server to load, in
	// seconds (default 300): a model downloaded on first use takes longer.
	StartTimeout int `json:"start_timeout"`
	// Prefetch are Hugging Face models setup downloads with the backend, so
	// the first sentence does not wait for them.
	Prefetch []string `json:"prefetch"`
	// Expressive is what it does with voice's sound tags and moods (see
	// core.Expressive): sounds, emotion, moods, instruct.
	Expressive map[string]any `json:"expressive"`
	// Env is the Python environment it runs in (envs\<env>): another
	// backend's, when it needs the same packages; empty is its own.
	Env string `json:"env"`
	// GPUs are the graphics cards it runs on (nvidia, amd); empty is any.
	// The Integrations window greys out Install on a PC with none of them.
	GPUs []string `json:"gpus"`
	// CPU: it runs on the processor, on any PC. Setup gives it the CPU
	// build of PyTorch whatever the card, and voice never counts it against
	// graphics memory or frees GPU voices for it.
	CPU bool `json:"cpu"`
	// VRAM is about how much graphics memory it takes loaded, in GB: the
	// starting estimate voice keeps GPU voices within a budget by, until it
	// has measured the real use.
	VRAM float64 `json:"vram"`
}

// Memory is the backend's estimated graphics memory in GB with the option
// value it runs with (a model size).
func (b Backend) Memory(set map[string]string) float64 {
	if b.Option != nil {
		v := b.Value(set)
		for _, c := range b.Option.Choices {
			if c.Value == v && c.VRAM > 0 {
				return c.VRAM
			}
		}
	}
	return b.VRAM
}

// GPUNames says which cards it needs, for people: "an NVIDIA graphics card",
// "an NVIDIA or AMD graphics card".
func (b Backend) GPUNames() string {
	names := map[string]string{"nvidia": "NVIDIA", "amd": "AMD", "intel": "Intel"}
	var out []string
	for _, g := range b.GPUs {
		if n, ok := names[g]; ok {
			out = append(out, n)
		} else {
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "an " + strings.Join(out, " or ") + " graphics card" // an NVIDIA, an AMD, an Intel
}

// RunsOn reports whether one of the cards found (nvidia, amd, intel) runs
// it; with none found (unknown), it does.
func (b Backend) RunsOn(found map[string]bool) bool {
	if len(b.GPUs) == 0 || len(found) == 0 {
		return true
	}
	for _, g := range b.GPUs {
		if found[g] {
			return true
		}
	}
	return false
}

// SourceKey is what an environment records for the backend's Source, to
// tell whether it has the current build: "" for none.
func (b Backend) SourceKey() string { return strings.Join(b.Source, " ") }

// EnvName is the environment the backend runs in.
func (b Backend) EnvName() string {
	if b.Env != "" {
		return b.Env
	}
	return b.Name
}

var backends = func() []Backend {
	var f struct {
		Backends []Backend `json:"backends"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		panic("sidecar/backends.json: " + err.Error())
	}
	return f.Backends
}()

// Backends lists them in the file's order.
func Backends() []Backend { return backends }

// Find returns the backend of that name.
func Find(name string) (Backend, bool) {
	for _, b := range backends {
		if b.Name == name {
			return b, true
		}
	}
	return Backend{}, false
}

// PromptFormat is the kind of voice prompt file the engine of that name
// reads and writes: the backend PromptsAs names, else its own name; ""
// for an engine that keeps no prompts or is no backend.
func PromptFormat(name string) string {
	b, ok := Find(name)
	if !ok || !b.Prompts {
		return ""
	}
	if b.PromptsAs != "" {
		return b.PromptsAs
	}
	return b.Name
}

// Names lists the backends, the hidden ones too when all is set.
func Names(all bool) []string {
	var out []string
	for _, b := range backends {
		if all || !b.Hidden {
			out = append(out, b.Name)
		}
	}
	return out
}

// Value is the option's value from the user's settings, or its default; an
// unknown value is kept (a model the list does not name yet).
func (b Backend) Value(set map[string]string) string {
	if b.Option == nil {
		return ""
	}
	if v := strings.TrimSpace(set[b.Option.Key]); v != "" {
		return v
	}
	return b.Option.Default
}

// Engine is the [engines.<name>] table voice uses for the backend: a server
// on its port that voice starts on first use and that exits after ten idle
// minutes, freeing the GPU.
func (b Backend) Engine(set map[string]string) map[string]any {
	port := strconv.Itoa(b.Port)
	start := []any{"{python:" + b.EnvName() + "}", "-m", "voice_sidecar", "--backend", b.Name, "--port", port, "--idle-seconds", "600"}
	if b.Option != nil {
		start = append(start, "--option", b.Option.Key+"="+b.Value(set))
	}
	e := map[string]any{"type": "http", "url": "http://127.0.0.1:" + port, "start": start}
	if len(b.Extra) > 0 {
		e["extra"] = b.Extra
	}
	if b.Batch {
		e["batch"] = true
	}
	if len(b.Expressive) > 0 {
		e["expressive"] = b.Expressive
	}
	if b.StartTimeout > 0 {
		e["start_timeout"] = int64(b.StartTimeout)
	}
	return e
}

// Summary names the backends for help texts: "chatterbox, f5, xtts, qwen3".
func Summary() string { return strings.Join(Names(false), ", ") }

// Check reports a problem with the list itself, for tests.
func Check() error {
	ports := map[int]string{}
	for _, b := range backends {
		if b.Name == "" || b.Label == "" || b.Port == 0 {
			return fmt.Errorf("backend %+v: name, label and port are required", b)
		}
		if other, ok := ports[b.Port]; ok {
			return fmt.Errorf("%s and %s share port %d", b.Name, other, b.Port)
		}
		ports[b.Port] = b.Name
		if b.CPU && (len(b.GPUs) > 0 || b.VRAM > 0 || b.Env != "") {
			return fmt.Errorf("%s runs on the processor: no gpus, no vram, and an environment of its own (it gets the CPU PyTorch)", b.Name)
		}
		if o, ok := Find(b.EnvName()); ok && o.SourceKey() != b.SourceKey() {
			return fmt.Errorf("%s runs in %s's environment with another source build", b.Name, o.Name)
		}
		if b.PromptsAs != "" {
			if o, ok := Find(b.PromptsAs); !ok || !o.Prompts || !b.Prompts {
				return fmt.Errorf("%s: prompts_as %q needs both to keep prompts", b.Name, b.PromptsAs)
			}
		}
		if o := b.Option; o != nil {
			found := false
			for _, c := range o.Choices {
				found = found || c.Value == o.Default
			}
			if o.Key == "" || !found {
				return fmt.Errorf("%s: option %q needs a key and a default among its choices", b.Name, o.Key)
			}
		}
	}
	return nil
}
