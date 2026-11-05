package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"voice/internal/clones"
	"voice/internal/core"
	"voice/internal/service"
	"voice/sidecar"
)

var cloneUsage = `usage:
  voice clone record <name> [-seconds 12] [-engine name] [-rights who] [-replace]
      three ticks count down, a chime starts and ends the recording; read a few
      natural sentences aloud. The clip is checked (level, clipping, noise) and added.
  voice clone add <name> <clip> [-text transcript] [-engine name] [-rights who] [-replace]
      <clip> may be a reference.json with "audio" and "text" (as dataset tools write).
  voice clone prompt <name>
      makes the voice's prompt again (engines that keep one: qwen3), e.g. after
      the model size changed. With a prompt the voice speaks without its recording.
  voice clone list
  voice clone preview <name> [-out file.wav] [text]
  voice clone text <name> <transcript>
  voice clone remove <name>

Engines: pocket (default, CPU), zipvoice (CPU, needs -text), and on the GPU
sidecar once set up: ` + sidecar.Summary() + `. Those needing the transcript
(-text, or ` + "`voice clone text`" + `): zipvoice, ` + strings.Join(transcriptEngines(), ", ") + `.

Each cloned voice also becomes a preset of the same name.
Only clone voices you have the right to use.`

func transcriptEngines() []string {
	var out []string
	for _, b := range sidecar.Backends() {
		if b.Transcript && !b.Hidden {
			out = append(out, b.Name)
		}
	}
	return out
}

func runClone(args []string) error {
	if len(args) == 0 {
		return errors.New(cloneUsage)
	}
	sub, rest := args[0], args[1:]
	fs, cfgPath := flags("clone " + sub)
	text := fs.String("text", "", "transcript of the clip (needed by zipvoice, f5 and qwen3)")
	engineName := fs.String("engine", "", "preferred engine (default pocket)")
	rights := fs.String("rights", "", "who confirmed the right to use this voice")
	replace := fs.Bool("replace", false, "overwrite an existing voice")
	out := fs.String("out", "", "preview: write a WAV file instead of playing")
	seconds := fs.Float64("seconds", 12, "record: length of the recording")
	// Flags may follow the positional arguments.
	var pos []string
	for len(rest) > 0 {
		fs.Parse(rest)
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return err
	}
	ctx := context.Background()
	c := newClient(*cfgPath)
	switch sub {
	case "add", "record":
		args := service.CloneArgs{Action: sub, Transcript: *text, Engine: *engineName,
			RightsConfirmed: true, Rights: dirOr(*rights, "confirmed by the CLI user"), Replace: *replace}
		switch {
		case sub == "record" && len(pos) == 1:
			args.Name, args.Seconds = pos[0], *seconds
			fmt.Printf("Recording %.0f s after three ticks and a chime. Read a few natural sentences aloud.\n", *seconds)
		case sub == "add" && len(pos) == 2:
			clip, err := filepath.Abs(pos[1])
			if err != nil {
				return err
			}
			args.Name, args.ClipPath = pos[0], clip
		default:
			return errors.New(cloneUsage)
		}
		// The background process records or imports the clip so its preset list
		// and recognizer stay in step; the person running the CLI confirms rights.
		raw, err := c.Clone(ctx, args)
		if err != nil {
			return err
		}
		m, _ := raw.(map[string]any)
		fmt.Printf("added %v: %v s, engine %v, preset %v\n", m["name"], m["seconds"], m["engine"], m["preset"])
		if q, ok := m["quality"].([]any); ok {
			for _, w := range q {
				fmt.Printf("quality: %v\n", w)
			}
		}
		if pr, ok := m["prompt"].(map[string]any); ok {
			fmt.Printf("voice prompt: %v (%v %v); it speaks without its recording now\n", pr["file"], pr["model"], pr["revision"])
		}
		if w, ok := m["warning"].(string); ok {
			fmt.Println("warning: " + w)
		}
		if t, _ := m["transcript"].(string); t != "" {
			fmt.Printf("transcript: %s\n", t)
		} else {
			fmt.Println("no transcript: fine for pocket; zipvoice, " + strings.Join(transcriptEngines(), ", ") + " need `voice clone text`")
		}
	case "list":
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		defer w.Flush()
		for _, p := range clones.List() {
			hasText := "no transcript"
			if p.Text != "" {
				hasText = "transcript"
			}
			prompt := ""
			if p.Prompt != "" {
				prompt = "prompt (" + p.PromptModel + ")"
			}
			fmt.Fprintf(w, "%s\t%.1f s\t%s\t%s\t%s\t%s\n", p.Name, p.Duration, p.Engine, hasText, prompt, p.Source)
		}
	case "prompt":
		if len(pos) != 1 {
			return errors.New(cloneUsage)
		}
		raw, err := c.Clone(ctx, service.CloneArgs{Action: "make_prompt", Name: pos[0]})
		if err != nil {
			return err
		}
		m, _ := raw.(map[string]any)
		pr, _ := m["prompt"].(map[string]any)
		fmt.Printf("voice prompt for %s: %v (%v %v, from %v)\n", pos[0], pr["file"], pr["model"], pr["revision"], pr["from"])
	case "text":
		if len(pos) < 2 {
			return errors.New(cloneUsage)
		}
		_, err := c.Clone(ctx, service.CloneArgs{Action: "set_transcript", Name: pos[0], Transcript: strings.Join(pos[1:], " ")})
		return err
	case "remove":
		if len(pos) != 1 {
			return errors.New(cloneUsage)
		}
		_, err := c.Clone(ctx, service.CloneArgs{Action: "remove", Name: pos[0]})
		return err
	case "preview":
		if len(pos) < 1 {
			return errors.New(cloneUsage)
		}
		if _, err := clones.Get(pos[0]); err != nil {
			return err
		}
		sample := "Hi, this is how I sound now. I finished the task, and I'm ready for the next one."
		if len(pos) > 1 {
			sample = strings.Join(pos[1:], " ")
		}
		if *out == "" {
			_, err := c.Speak(ctx, service.SpeakArgs{Text: sample, Preset: core.ClonePresetName(pos[0]), Lang: "en", Wait: true})
			return err
		}
		sp := core.NewSpeaker(cfg)
		defer sp.Close()
		sp.RecordTo(*out)
		t, err := sp.Speak(core.Request{Text: sample, Preset: core.ClonePresetName(pos[0]), Lang: "en"})
		if err != nil {
			return err
		}
		select {
		case res := <-t.Done:
			if res.Err != nil {
				return res.Err
			}
			fmt.Fprintln(os.Stderr, "spoken by engine", res.Engine)
		case <-time.After(2 * time.Minute):
			return errors.New("preview timed out")
		}
		sp.Close()
		if msg := sp.Err(); *out != "" && msg != "" {
			return errors.New(msg)
		}
	default:
		return errors.New(cloneUsage)
	}
	return nil
}
