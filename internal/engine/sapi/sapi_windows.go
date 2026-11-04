// Package sapi renders speech with Windows SAPI 5 into memory, so the audio
// goes through the same queue, lock and effects as every other engine.
package sapi

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"

	"voice/internal/engine"
)

const (
	formatType22kHz16BitMono = 22
	sampleRate               = 22050
	flagIsNotXML             = 16
)

// LCIDs as SAPI reports them in the token "Language" attribute.
var langLCIDs = map[string][]string{
	"pl": {"415"}, "en": {"409", "809", "c09", "1009"}, "de": {"407"},
	"fr": {"40c", "c0c"}, "es": {"40a", "c0a", "80a"}, "it": {"410"},
	"pt": {"416", "816"}, "ru": {"419"}, "uk": {"422"}, "cs": {"405"},
	"ja": {"411"}, "zh": {"804", "404"},
}

func init() { engine.Register("sapi", New) }

type token struct {
	disp   *ole.IDispatch
	name   string
	lcids  []string
	gender string
}

type job struct {
	run  func() error
	done chan error
}

type Engine struct {
	jobs chan job
	quit chan struct{}
}

// New starts the COM worker. SAPI objects are apartment-bound, so every call
// runs on one locked OS thread.
func New(_ string, _ engine.Config) (engine.Engine, error) {
	e := &Engine{jobs: make(chan job), quit: make(chan struct{})}
	ready := make(chan error, 1)
	go e.loop(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Engine) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		var oleErr *ole.OleError
		// S_FALSE: already initialized on this thread, still usable.
		if !errors.As(err, &oleErr) || oleErr.Code() != 1 {
			ready <- fmt.Errorf("sapi: CoInitializeEx: %w", err)
			return
		}
	}
	defer ole.CoUninitialize()
	ready <- nil
	for {
		select {
		case j := <-e.jobs:
			j.done <- j.run()
		case <-e.quit:
			return
		}
	}
}

func (e *Engine) do(ctx context.Context, fn func() error) error {
	j := job{run: fn, done: make(chan error, 1)}
	select {
	case e.jobs <- j:
	case <-ctx.Done():
		return ctx.Err()
	case <-e.quit:
		return errors.New("sapi: engine closed")
	}
	// SAPI Speak is synchronous and cannot be interrupted from here; the
	// sentence is short, so waiting for it keeps the COM thread consistent.
	return <-j.done
}

func (e *Engine) Close() error {
	select {
	case <-e.quit:
	default:
		close(e.quit)
	}
	return nil
}

func (e *Engine) Voices(ctx context.Context) ([]engine.VoiceInfo, error) {
	var out []engine.VoiceInfo
	err := e.do(ctx, func() error {
		voice, err := newVoice()
		if err != nil {
			return err
		}
		defer voice.Release()
		tokens, err := listTokens(voice)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			out = append(out, engine.VoiceInfo{Name: t.name, Lang: langOf(t.lcids), Gender: t.gender})
			t.disp.Release()
		}
		return nil
	})
	return out, err
}

func (e *Engine) Synthesize(ctx context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	if req.Voice.Ref != "" {
		return engine.ErrNoCloning
	}
	var pcm []float32
	err := e.do(ctx, func() (err error) {
		pcm, err = speak(req)
		return err
	})
	if err != nil {
		return err
	}
	return sink(engine.Chunk{PCM: pcm, SampleRate: sampleRate})
}

func newVoice() (*ole.IDispatch, error) {
	unk, err := oleutil.CreateObject("SAPI.SpVoice")
	if err != nil {
		return nil, fmt.Errorf("sapi: create SpVoice: %w", err)
	}
	defer unk.Release()
	return unk.QueryInterface(ole.IID_IDispatch)
}

func speak(req engine.Request) ([]float32, error) {
	voice, err := newVoice()
	if err != nil {
		return nil, err
	}
	defer voice.Release()

	tokens, err := listTokens(voice)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, t := range tokens {
			t.disp.Release()
		}
	}()
	if t := pickToken(tokens, req.Voice.Primary(), req.Lang); t != nil {
		if _, err := oleutil.PutPropertyRef(voice, "Voice", t.disp); err != nil {
			return nil, fmt.Errorf("sapi: select voice %q: %w", t.name, err)
		}
	}
	if _, err := oleutil.PutProperty(voice, "Rate", rateFor(req.Speed)); err != nil {
		return nil, fmt.Errorf("sapi: set rate: %w", err)
	}

	unk, err := oleutil.CreateObject("SAPI.SpMemoryStream")
	if err != nil {
		return nil, fmt.Errorf("sapi: create SpMemoryStream: %w", err)
	}
	defer unk.Release()
	stream, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}
	defer stream.Release()
	format, err := oleutil.GetProperty(stream, "Format")
	if err != nil {
		return nil, fmt.Errorf("sapi: stream format: %w", err)
	}
	formatDisp := format.ToIDispatch()
	defer formatDisp.Release()
	if _, err := oleutil.PutProperty(formatDisp, "Type", formatType22kHz16BitMono); err != nil {
		return nil, fmt.Errorf("sapi: set stream format: %w", err)
	}
	if _, err := oleutil.PutPropertyRef(stream, "Format", formatDisp); err != nil {
		return nil, fmt.Errorf("sapi: apply stream format: %w", err)
	}
	if _, err := oleutil.PutPropertyRef(voice, "AudioOutputStream", stream); err != nil {
		return nil, fmt.Errorf("sapi: set output stream: %w", err)
	}
	if _, err := oleutil.CallMethod(voice, "Speak", req.Text, flagIsNotXML); err != nil {
		return nil, fmt.Errorf("sapi: speak: %w", err)
	}
	data, err := oleutil.CallMethod(stream, "GetData")
	if err != nil {
		return nil, fmt.Errorf("sapi: read stream: %w", err)
	}
	defer data.Clear()
	arr := data.ToArray()
	if arr == nil {
		return nil, errors.New("sapi: stream returned no audio")
	}
	raw := arr.ToByteArray()
	pcm := make([]float32, len(raw)/2)
	for i := range pcm {
		pcm[i] = float32(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
	}
	return pcm, nil
}

func listTokens(voice *ole.IDispatch) ([]token, error) {
	res, err := oleutil.CallMethod(voice, "GetVoices")
	if err != nil {
		return nil, fmt.Errorf("sapi: list voices: %w", err)
	}
	list := res.ToIDispatch()
	defer list.Release()
	count, err := oleutil.GetProperty(list, "Count")
	if err != nil {
		return nil, err
	}
	n := int(count.Val)
	out := make([]token, 0, n)
	for i := 0; i < n; i++ {
		item, err := oleutil.CallMethod(list, "Item", i)
		if err != nil {
			continue
		}
		disp := item.ToIDispatch()
		t := token{disp: disp}
		if d, err := oleutil.CallMethod(disp, "GetDescription"); err == nil {
			t.name = d.ToString()
		}
		if l, err := oleutil.CallMethod(disp, "GetAttribute", "Language"); err == nil {
			for _, id := range strings.Split(l.ToString(), ";") {
				t.lcids = append(t.lcids, strings.ToLower(strings.TrimSpace(id)))
			}
		}
		if g, err := oleutil.CallMethod(disp, "GetAttribute", "Gender"); err == nil {
			t.gender = strings.ToLower(g.ToString())
		}
		out = append(out, t)
	}
	return out, nil
}

// pickToken matches the requested name first, then the language. Returning
// nil keeps the system default voice.
func pickToken(tokens []token, name, lang string) *token {
	if name != "" {
		needle := strings.ToLower(name)
		for i := range tokens {
			if strings.Contains(strings.ToLower(tokens[i].name), needle) {
				return &tokens[i]
			}
		}
	}
	for _, want := range langLCIDs[lang] {
		for i := range tokens {
			for _, have := range tokens[i].lcids {
				if have == want {
					return &tokens[i]
				}
			}
		}
	}
	return nil
}

func langOf(lcids []string) string {
	for lang, ids := range langLCIDs {
		for _, id := range ids {
			for _, have := range lcids {
				if have == id {
					return lang
				}
			}
		}
	}
	return strings.Join(lcids, ";")
}

// rateFor maps a speed multiplier to SAPI's -10..10 scale, where +10 is
// roughly three times faster.
func rateFor(speed float64) int {
	if speed <= 0 {
		return 0
	}
	r := int(math.Round(10 * math.Log(speed) / math.Log(3)))
	return max(-10, min(10, r))
}
