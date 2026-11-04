// Package command runs any external TTS program that produces WAV audio.
//
//	[engines.espeak]
//	type = "exec"
//	command = "espeak-ng"
//	args = ["-v", "{voice}", "-s", "{wpm}", "--stdout", "{text}"]
//
// Placeholders: {text} {voice} {lang} {speed} {wpm} {out}, and for cloning
// commands {ref} (reference clip path) and {ref_text} (its transcript).
// Without {text} the text goes to stdin; with {out} the WAV is read from that
// temporary file, otherwise from stdout.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"voice/internal/audio"
	"voice/internal/engine"
)

func init() { engine.Register("exec", New) }

type Engine struct {
	name    string
	command string
	args    []string
	voices  []string
	voiceBy map[string]string // lang -> default voice
	usesRef bool
}

func New(name string, cfg engine.Config) (engine.Engine, error) {
	cmd := cfg.String("command")
	if cmd == "" {
		return nil, fmt.Errorf("engine %q: exec needs command", name)
	}
	e := &Engine{name: name, command: cmd, args: cfg.Strings("args"), voices: cfg.Strings("voices"), voiceBy: map[string]string{}}
	// A command that takes {ref} clones: it gets the clip path and transcript.
	for _, a := range e.args {
		e.usesRef = e.usesRef || strings.Contains(a, "{ref}")
	}
	if byLang, ok := cfg["lang_voices"].(map[string]any); ok {
		for lang, v := range byLang {
			if s, ok := v.(string); ok {
				e.voiceBy[lang] = s
			}
		}
	}
	return e, nil
}

func (e *Engine) Close() error { return nil }

func (e *Engine) Voices(context.Context) ([]engine.VoiceInfo, error) {
	out := make([]engine.VoiceInfo, 0, len(e.voices))
	for _, v := range e.voices {
		out = append(out, engine.VoiceInfo{Name: v})
	}
	return out, nil
}

func (e *Engine) Synthesize(ctx context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	voice := req.Voice.Primary()
	if req.Voice.Ref != "" && !e.usesRef {
		return engine.ErrNoCloning
	}
	if voice == "" {
		voice = e.voiceBy[req.Lang]
	}
	if voice == "" {
		voice = req.Lang
	}
	speed := req.Speed
	if speed <= 0 {
		speed = 1
	}

	var outPath string
	usesText := false
	args := make([]string, len(e.args))
	for i, a := range e.args {
		if strings.Contains(a, "{out}") && outPath == "" {
			f, err := os.CreateTemp("", "voice-*.wav")
			if err != nil {
				return err
			}
			outPath = f.Name()
			f.Close()
			defer os.Remove(outPath)
		}
		usesText = usesText || strings.Contains(a, "{text}")
		args[i] = strings.NewReplacer(
			"{text}", req.Text,
			"{voice}", voice,
			"{lang}", req.Lang,
			"{speed}", strconv.FormatFloat(speed, 'f', 2, 64),
			"{wpm}", strconv.Itoa(int(175*speed)),
			"{out}", outPath,
			"{ref}", req.Voice.Ref,
			"{ref_text}", req.Voice.RefText,
		).Replace(a)
	}

	cmd := exec.CommandContext(ctx, e.command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if !usesText {
		cmd.Stdin = strings.NewReader(req.Text)
	}
	hideWindow(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("engine %q: %w: %s", e.name, err, strings.TrimSpace(stderr.String()))
	}
	wav := stdout.Bytes()
	if outPath != "" {
		var err error
		if wav, err = os.ReadFile(outPath); err != nil {
			return err
		}
	}
	if len(wav) == 0 {
		return errors.New("engine " + strconv.Quote(e.name) + ": command produced no audio")
	}
	pcm, rate, err := audio.DecodeWAV(wav)
	if err != nil {
		return fmt.Errorf("engine %q: %w", e.name, err)
	}
	return sink(engine.Chunk{PCM: pcm, SampleRate: rate})
}
