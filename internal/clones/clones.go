// Package clones stores cloned voice profiles: a cleaned reference clip, its
// transcript and where it came from.
package clones

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"voice/internal/audio"
)

// Dir holds one directory per profile; set at startup.
var Dir string

const (
	refRate    = 24000
	minSpeech  = 3.0  // seconds of speech a clip must have
	maxSpeech  = 15.0 // longer clips are cut; models use a few seconds anyway
	targetPeak = 0.9
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

type Profile struct {
	Name     string    `toml:"-"`
	Ref      string    `toml:"-"` // path to ref.wav
	Text     string    `toml:"-"` // transcript of ref.wav, may be empty
	Source   string    `toml:"source"`
	Added    time.Time `toml:"added"`
	Engine   string    `toml:"engine"` // preferred engine
	Rights   string    `toml:"rights"` // who confirmed the right to use this voice
	Duration float64   `toml:"duration_seconds"`
	// Original is the recording as it came (source.<ext>), kept as the
	// source copy: ref.wav is trimmed and resampled from it.
	Original string `toml:"original,omitempty"`
	// Prompt is a voice prompt file (prompt.pt) an engine made from the
	// recording once; with it, speech needs no recording. Its engine, model
	// and model revision say what it works with.
	Prompt         string `toml:"prompt,omitempty"`
	PromptEngine   string `toml:"prompt_engine,omitempty"`
	PromptModel    string `toml:"prompt_model,omitempty"`
	PromptRevision string `toml:"prompt_revision,omitempty"`
	PromptPath     string `toml:"-"` // Prompt as a full path, when there is one
	OriginalPath   string `toml:"-"`
}

func dir(name string) string { return filepath.Join(Dir, name) }

func ValidName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("voice name %q: use 1-40 lowercase letters, digits, - or _", name)
	}
	return nil
}

// Add imports a clip as a new profile. Audio other than WAV needs ffmpeg on
// PATH. The clip is converted to mono 24 kHz, trimmed of silence, cut to at
// most 15 s of speech and peak-normalized.
func Add(name, src, text, engineName, rights string, replace bool) (Profile, error) {
	if err := ValidName(name); err != nil {
		return Profile{}, err
	}
	if Dir == "" {
		return Profile{}, errors.New("voice profile directory not set")
	}
	if _, err := os.Stat(dir(name)); err == nil && !replace {
		return Profile{}, fmt.Errorf("voice %q already exists; remove it first or replace it", name)
	}
	pcm, rate, err := decode(src)
	if err != nil {
		return Profile{}, err
	}
	pcm = audio.Resample(pcm, rate, refRate)
	pcm, err = clean(pcm, refRate)
	if err != nil {
		return Profile{}, err
	}
	if engineName == "" {
		engineName = "pocket" // needs no transcript; ZipVoice is opt-in
	}
	p := Profile{
		Name: name, Text: strings.TrimSpace(text), Source: filepath.Base(src), Added: time.Now().UTC().Truncate(time.Second),
		Engine: engineName, Rights: rights, Duration: math.Round(float64(len(pcm))/refRate*10) / 10,
	}
	stage := dir(name) + ".new"
	os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return Profile{}, err
	}
	var meta bytes.Buffer
	toml.NewEncoder(&meta).Encode(p)
	// The recording as it came stays as the source copy.
	if orig, err := os.ReadFile(src); err == nil {
		ext := strings.ToLower(filepath.Ext(src))
		if ext == "" || len(ext) > 6 {
			ext = ".wav"
		}
		p.Original = "source" + ext
		meta.Reset()
		toml.NewEncoder(&meta).Encode(p)
		if err := os.WriteFile(filepath.Join(stage, p.Original), orig, 0o644); err != nil {
			os.RemoveAll(stage)
			return Profile{}, err
		}
	}
	files := map[string][]byte{"ref.wav": audio.EncodeWAV(pcm, refRate), "voice.toml": meta.Bytes()}
	if p.Text != "" {
		files["ref.txt"] = []byte(p.Text + "\n")
	}
	for f, b := range files {
		if err := os.WriteFile(filepath.Join(stage, f), b, 0o644); err != nil {
			os.RemoveAll(stage)
			return Profile{}, err
		}
	}
	os.RemoveAll(dir(name))
	if err := os.Rename(stage, dir(name)); err != nil {
		return Profile{}, err
	}
	return Get(name)
}

// ErrNotFound: no cloned voice of that name is stored (never made, or removed).
var ErrNotFound = errors.New("no cloned voice")

func Get(name string) (Profile, error) {
	if err := ValidName(name); err != nil {
		return Profile{}, err
	}
	d := dir(name)
	var p Profile
	if _, err := toml.DecodeFile(filepath.Join(d, "voice.toml"), &p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Profile{}, fmt.Errorf("%w %q; see `voice clone list`", ErrNotFound, name)
		}
		return Profile{}, fmt.Errorf("voice %q: %w", name, err)
	}
	p.Name = name
	p.Ref = filepath.Join(d, "ref.wav")
	if b, err := os.ReadFile(filepath.Join(d, "ref.txt")); err == nil {
		p.Text = strings.TrimSpace(string(b))
	}
	if p.Prompt != "" {
		p.PromptPath = filepath.Join(d, filepath.Base(p.Prompt))
	}
	if p.Original != "" {
		p.OriginalPath = filepath.Join(d, filepath.Base(p.Original))
	}
	return p, nil
}

// PromptFile is where a voice's prompt goes.
func PromptFile(name string) string { return filepath.Join(dir(name), "prompt.pt") }

// SetPrompt records the prompt an engine saved to PromptFile(name).
func SetPrompt(name, engine, model, revision string) error {
	p, err := Get(name)
	if err != nil {
		return err
	}
	p.Prompt, p.PromptEngine, p.PromptModel, p.PromptRevision = "prompt.pt", engine, model, revision
	var meta bytes.Buffer
	if err := toml.NewEncoder(&meta).Encode(p); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir(name), "voice.toml"), meta.Bytes(), 0o644)
}

// Reference reads a reference description: a JSON file with the recording
// ("audio", relative to the file's folder when relative) and what it says
// ("text"), as dataset tools write them.
func Reference(path string) (clip, text string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var r struct {
		Audio string `json:"audio"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")), &r); err != nil {
		return "", "", fmt.Errorf("%s: %w", path, err)
	}
	if r.Audio == "" {
		return "", "", fmt.Errorf("%s names no audio", path)
	}
	if !filepath.IsAbs(r.Audio) {
		r.Audio = filepath.Join(filepath.Dir(path), r.Audio)
	}
	return r.Audio, strings.TrimSpace(r.Text), nil
}

func List() []Profile {
	entries, _ := os.ReadDir(Dir)
	var out []Profile
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".new") {
			continue
		}
		if p, err := Get(e.Name()); err == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func Remove(name string) error {
	if _, err := Get(name); err != nil {
		return err
	}
	return os.RemoveAll(dir(name))
}

// SetText stores a transcript for an existing profile.
func SetText(name, text string) error {
	if _, err := Get(name); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir(name), "ref.txt"), []byte(strings.TrimSpace(text)+"\n"), 0o644)
}

func decode(src string) ([]float32, int, error) {
	if strings.EqualFold(filepath.Ext(src), ".wav") {
		b, err := os.ReadFile(src)
		if err != nil {
			return nil, 0, err
		}
		if pcm, rate, err := audio.DecodeWAV(b); err == nil {
			return pcm, rate, nil
		}
		// Compressed WAV variants go through ffmpeg below.
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, 0, fmt.Errorf("%s: only PCM WAV can be read without ffmpeg on PATH", src)
	}
	var out, stderr bytes.Buffer
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", src, "-ac", "1", "-ar", fmt.Sprint(refRate), "-f", "wav", "-")
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("ffmpeg %s: %v: %s", src, err, strings.TrimSpace(stderr.String()))
	}
	return audio.DecodeWAV(out.Bytes())
}

// Check judges a raw recording before it becomes a voice: problems make the
// clip unusable, warnings make the clone worse but still work.
func Check(pcm []float32, rate int) (problems, warnings []string) {
	frame := rate / 50 // 20 ms
	n := len(pcm) / frame
	if n == 0 {
		return []string{"the recording is empty"}, nil
	}
	var peak float32
	clipped := 0
	for _, s := range pcm {
		a := float32(math.Abs(float64(s)))
		peak = max(peak, a)
		if a >= 0.99 {
			clipped++
		}
	}
	energy := make([]float64, n)
	for i := range energy {
		var sum float64
		for _, s := range pcm[i*frame : (i+1)*frame] {
			sum += float64(s) * float64(s)
		}
		energy[i] = math.Sqrt(sum / float64(frame))
	}
	sorted := append([]float64(nil), energy...)
	sort.Float64s(sorted)
	floor, loud := sorted[n/10], sorted[n*9/10]
	switch {
	case peak < 0.02:
		problems = append(problems, "almost silent: check the microphone (voice paths, [listen] input_device) and its level")
	case peak < 0.1:
		warnings = append(warnings, "quiet: speak closer to the microphone or raise its level")
	}
	if frac := float64(clipped) / float64(len(pcm)); frac > 0.01 {
		problems = append(problems, "heavily clipped: lower the microphone level or move back")
	} else if frac > 0.001 {
		warnings = append(warnings, "slightly clipped: lower the microphone level a little")
	}
	// The quietest tenth is the room, the loudest tenth the voice.
	if floor > 0 && loud/floor < 6 {
		warnings = append(warnings, "noisy background (under 15 dB to the voice): record in a quieter room")
	}
	return problems, warnings
}

// clean trims leading and trailing silence, caps the length at a quiet point
// and normalizes the peak.
func clean(pcm []float32, rate int) ([]float32, error) {
	frame := rate / 50 // 20 ms
	n := len(pcm) / frame
	if n == 0 {
		return nil, errors.New("clip is empty")
	}
	energy := make([]float64, n)
	var peak float64
	for i := range energy {
		var sum float64
		for _, s := range pcm[i*frame : (i+1)*frame] {
			sum += float64(s) * float64(s)
		}
		energy[i] = math.Sqrt(sum / float64(frame))
		peak = math.Max(peak, energy[i])
	}
	threshold := math.Max(0.005, peak*0.08)
	first, last := -1, -1
	voiced := 0
	for i, e := range energy {
		if e >= threshold {
			if first < 0 {
				first = i
			}
			last = i
			voiced++
		}
	}
	if first < 0 || float64(voiced)*0.02 < minSpeech {
		return nil, fmt.Errorf("clip has under %.0f s of clear speech; use a longer, cleaner recording", minSpeech)
	}
	// Keep 150 ms of room around the speech.
	first = max(0, first-7)
	last = min(n-1, last+7)
	if limit := first + int(maxSpeech*50); last > limit {
		// End at the quietest frame in the last two seconds before the limit.
		cut := limit
		for i := limit - 100; i <= limit; i++ {
			if i > first && energy[i] < energy[cut] {
				cut = i
			}
		}
		last = cut
	}
	out := append([]float32(nil), pcm[first*frame:(last+1)*frame]...)
	var maxAbs float32
	for _, s := range out {
		maxAbs = max(maxAbs, float32(math.Abs(float64(s))))
	}
	if maxAbs > 0 {
		g := targetPeak / maxAbs
		for i := range out {
			out[i] *= g
		}
	}
	// Short fades avoid clicks at the cut points.
	fade := min(len(out)/2, rate/100)
	for i := 0; i < fade; i++ {
		g := float32(i) / float32(fade)
		out[i] *= g
		out[len(out)-1-i] *= g
	}
	return out, nil
}
