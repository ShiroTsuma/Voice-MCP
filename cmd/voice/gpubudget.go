package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"voice/internal/preset"
	"voice/sidecar"
)

// A budget for the GPU voices (serve.gpu_budget): before a GPU voice's server
// loads its model, voice frees the least recently used other GPU voices until
// it fits, or all of them when only one may be loaded. A voice too big for the
// budget on its own still runs, alone: the budget is about voices sitting
// together, and it never takes a character's voice away.
//
// How much a voice takes starts as the estimate in sidecar/backends.json and
// becomes what its server reported once it has spoken (gpu-memory.json in the
// data folder), per model.

type gpuBudget struct {
	mu       sync.Mutex
	used     map[string]time.Time // when each engine last served a request
	measured map[string]float64   // engine|model -> GB its server took
	file     string
	config   func() *preset.Config
}

var gpus = &gpuBudget{used: map[string]time.Time{}, measured: map[string]float64{}}

func (g *gpuBudget) load(file string, config func() *preset.Config) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.file, g.config = file, config
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &g.measured)
	}
}

func (g *gpuBudget) use(engine string) {
	g.mu.Lock()
	g.used[engine] = time.Now()
	g.mu.Unlock()
}

// measure keeps what a server took (its peak), for the model it runs.
func (g *gpuBudget) measure(engine string, options map[string]string, mb int) {
	gb := float64(mb) / 1024
	key := memKey(engine, options["model"])
	g.mu.Lock()
	defer g.mu.Unlock()
	if gb <= g.measured[key] {
		return
	}
	g.measured[key] = gb
	if g.file != "" {
		if b, err := json.MarshalIndent(g.measured, "", "  "); err == nil {
			os.WriteFile(g.file, b, 0o644)
		}
	}
}

func memKey(engine, model string) string {
	if model == "" {
		return engine
	}
	return engine + "|" + model
}

// size is about how much graphics memory a GPU voice takes, in GB, and
// whether that was measured or is the estimate.
func (g *gpuBudget) size(b sidecar.Backend, cfg *preset.Config) (float64, bool) {
	model := ""
	if b.Option != nil && b.Option.Key == "model" {
		model = b.Value(cfg.Sidecar[b.Name])
	}
	g.mu.Lock()
	m := g.measured[memKey(b.Name, model)]
	g.mu.Unlock()
	if m > 0 {
		return m, true
	}
	return b.Memory(cfg.Sidecar[b.Name]), false
}

// budgetOf reads serve.gpu_budget: "off" (no limit), "one", or a number of GB.
func budgetOf(cfg *preset.Config) (mode string, limit float64) {
	v := strings.ToLower(strings.TrimSpace(cfg.Serve.GPUBudget))
	switch v {
	case "", "off", "none", "0":
		return "off", 0
	case "one", "1 voice":
		return "one", 0
	}
	if gb, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(v, "gb")), " "), 64); err == nil && gb > 0 {
		return "gb", gb
	}
	return "off", 0
}

// makeRoom runs before engine next's server loads its model.
func (g *gpuBudget) makeRoom(ctx context.Context, next string) {
	if g.config == nil {
		return
	}
	cfg := g.config()
	mode, limit := budgetOf(cfg)
	nb, ok := sidecar.Find(next)
	if mode == "off" || !ok {
		return
	}
	need, _ := g.size(nb, cfg)
	type loadedVoice struct {
		b   sidecar.Backend
		e   loadable
		gb  float64
		got time.Time
	}
	var loaded []loadedVoice
	sum := 0.0
	for _, b := range sidecar.Backends() {
		if b.Name == next || b.VRAM == 0 {
			continue
		}
		e, ok := gpuEngine(b.Name)
		if !ok {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		up := e.Loaded(c)
		cancel()
		if !up {
			continue
		}
		gb, _ := g.size(b, cfg)
		g.mu.Lock()
		at := g.used[b.Name]
		g.mu.Unlock()
		loaded = append(loaded, loadedVoice{b, e, gb, at})
		sum += gb
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].got.Before(loaded[j].got) })
	for _, l := range loaded {
		if mode == "gb" && sum+need <= limit {
			break
		}
		why := "only one GPU voice at a time"
		if mode == "gb" {
			why = fmt.Sprintf("to keep GPU voices within %s", gbText(limit))
		}
		logf("speech: unloading %s (about %s) for %s, %s", l.b.Label, gbText(l.gb), nb.Label, why)
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		l.e.Unload(c)
		cancel()
		sum -= l.gb
	}
	if mode == "gb" && need > limit {
		logf("speech: %s takes about %s, more than the %s budget: it runs alone", nb.Label, gbText(need), gbText(limit))
	}
}

// budgetSetting is serve.gpu_budget as the settings window shows it.
func budgetSetting(cfg *preset.Config) string {
	switch mode, limit := budgetOf(cfg); mode {
	case "gb":
		return strconv.FormatFloat(limit, 'f', -1, 64)
	default:
		return mode
	}
}

func gbText(gb float64) string {
	if gb == float64(int(gb)) {
		return fmt.Sprintf("%.0f GB", gb)
	}
	return fmt.Sprintf("%.1f GB", gb)
}

// gpuOverview is what the settings show about the GPU voices: the card's
// memory, the budget, which GPU voices the characters and the default voice
// use and how much each takes, and what they could take together. presets
// gives each GPU preset's size, so a window can show what a choice would
// change before it is saved.
func gpuOverview(cfg *preset.Config) map[string]any {
	mode, limit := budgetOf(cfg)
	type voiceUse struct {
		Engine   string   `json:"engine"`
		Label    string   `json:"label"`
		GB       float64  `json:"gb"`
		Measured bool     `json:"measured"`
		Users    []string `json:"users"`
	}
	uses := map[string]*voiceUse{}
	var order []string
	engineOf := func(name string) (sidecar.Backend, bool) {
		r, err := cfg.Resolve(name)
		if err != nil {
			return sidecar.Backend{}, false
		}
		b, ok := sidecar.Find(r.Engine)
		return b, ok && b.VRAM > 0
	}
	add := func(user, presetName string) {
		b, ok := engineOf(presetName)
		if !ok {
			return
		}
		u := uses[b.Name]
		if u == nil {
			gb, measured := gpus.size(b, cfg)
			u = &voiceUse{Engine: b.Name, Label: b.Label, GB: gb, Measured: measured}
			uses[b.Name] = u
			order = append(order, b.Name)
		}
		u.Users = append(u.Users, user)
	}
	add("the default voice", cfg.DefaultPreset)
	for _, c := range cfg.Characters {
		p := c.Preset
		if p == "" {
			p = cfg.DefaultPreset
		}
		add(c.Name, p)
	}
	voices := []*voiceUse{}
	peak := 0.0
	for _, name := range order {
		voices = append(voices, uses[name])
		peak += uses[name].GB
	}
	presets := map[string]any{}
	for _, name := range cfg.PresetNames() {
		if b, ok := engineOf(name); ok {
			gb, _ := gpus.size(b, cfg)
			presets[name] = map[string]any{"engine": b.Name, "label": b.Label, "gb": gb}
		}
	}
	// A smaller model size that would let them all fit, offered as a button.
	var hints []map[string]string
	if mode == "gb" && peak > limit {
		for _, u := range voices {
			b, _ := sidecar.Find(u.Engine)
			if b.Option == nil || b.Option.Key != "model" {
				continue
			}
			cur := b.Value(cfg.Sidecar[b.Name])
			for _, c := range b.Option.Choices {
				if c.Value != cur && c.VRAM > 0 && c.VRAM < u.GB && peak-u.GB+c.VRAM <= limit {
					hints = append(hints, map[string]string{
						"text":        fmt.Sprintf("%s at %s (about %s) would %s.", b.Label, c.Label, gbText(c.VRAM), map[bool]string{true: "fit the budget", false: "let them all fit"}[len(voices) == 1]),
						"label":       "Use " + c.Label,
						"integration": integrationOf(b.Name),
						"action":      "choose:" + c.Value,
					})
				}
			}
		}
	}
	return map[string]any{
		"total_gb": gpuTotalGB(), "mode": mode, "limit_gb": limit,
		"voices": voices, "peak_gb": peak, "presets": presets, "hints": hints,
	}
}

// integrationOf is the Integrations entry of a sidecar backend (its file's id).
func integrationOf(backend string) string {
	for _, dir := range []string{filepath.Join(exeDir(), "integrations")} {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		for _, p := range paths {
			if f := readIntegrationFile(p); f.Uses == "sidecar" && f.Backend == backend {
				return f.ID
			}
		}
	}
	return backend
}
