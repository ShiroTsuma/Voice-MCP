// Package listen records the user's spoken answer and transcribes it: the
// microphone feeds a voice activity detector, and finished speech goes to
// an offline recognizer.
package listen

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"voice/internal/audio"
	"voice/internal/core"
	"voice/internal/engine"
	"voice/internal/engine/sherpa"
	"voice/internal/preset"
)

const rate = 16000

type Listener struct {
	cfg    preset.Listen
	device string // output device for the chimes

	mu    sync.Mutex
	rec   *sherpa.Recognizer
	stale bool      // rec was loaded for another model; replace it when free
	used  time.Time // last recognition, for idle unloading
	busy  int       // recognitions or recordings in progress
}

func New(cfg *preset.Config) *Listener { return &Listener{cfg: cfg.Listen, device: cfg.Device} }

// SetModel switches the recognition model, its language (Whisper) and
// threads while voice runs: the loaded recognizer is replaced as soon as no
// recognition uses it, and the next one loads the new model.
func (l *Listener) SetModel(model, language string, threads int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if model == l.cfg.Model && language == l.cfg.Language && threads == l.cfg.Threads {
		return
	}
	l.cfg.Model, l.cfg.Language, l.cfg.Threads = model, language, threads
	if l.rec == nil {
		return
	}
	if l.busy == 0 {
		l.rec.Close()
		l.rec = nil
		return
	}
	l.stale = true
}

// modelDir: call with mu held.
func (l *Listener) modelDir() string {
	if filepath.IsAbs(l.cfg.Model) {
		return l.cfg.Model
	}
	return filepath.Join(engine.Dirs.Models, l.cfg.Model)
}

// Available reports whether a recognition model is installed.
func (l *Listener) Available() bool {
	l.mu.Lock()
	model, dir := l.cfg.Model, l.modelDir()
	l.mu.Unlock()
	_, err := os.Stat(dir)
	return model != "" && err == nil
}

func (l *Listener) recognizer() (*sherpa.Recognizer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.used = time.Now()
	// The model changed: drop the old recognizer once only the caller holds
	// the listener (callers take hold first; Warm does not).
	if l.rec != nil && l.stale && l.busy <= 1 {
		l.rec.Close()
		l.rec, l.stale = nil, false
	}
	if l.rec != nil {
		return l.rec, nil
	}
	if l.cfg.Model == "" {
		return nil, errors.New("no speech recognition model configured ([listen] model)")
	}
	threads := l.cfg.Threads
	if threads == 0 {
		threads = 4
	}
	r, err := sherpa.NewRecognizer(l.modelDir(), l.cfg.Language, threads)
	if err != nil {
		return nil, err
	}
	l.rec = r
	return r, nil
}

// Warm loads the recognizer ahead of the first question.
func (l *Listener) Warm() { l.recognizer() }

func (l *Listener) hold() func() {
	l.mu.Lock()
	l.busy++
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		l.busy--
		l.used = time.Now()
		l.mu.Unlock()
	}
}

// UnloadIdle frees the recognizer when nothing used it for longer than
// after; the next question loads it again.
func (l *Listener) UnloadIdle(after time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec == nil || l.busy > 0 || time.Since(l.used) < after {
		return false
	}
	l.rec.Close()
	l.rec, l.stale = nil, false
	return true
}

func (l *Listener) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec != nil {
		l.rec.Close()
		l.rec = nil
	}
}

// Transcribe recognizes complete audio at any sample rate.
func (l *Listener) Transcribe(pcm []float32, sampleRate int) (text, lang string, err error) {
	defer l.hold()()
	r, err := l.recognizer()
	if err != nil {
		return "", "", err
	}
	langs := l.languages()
	recMu.Lock()
	text, lang, err = recognize(r, pad(audio.Resample(pcm, sampleRate, rate)), langs)
	recMu.Unlock()
	return strings.TrimSpace(text), lang, err
}

// SetLanguages sets the languages the user speaks (see preset.Listen).
func (l *Listener) SetLanguages(langs []string) {
	l.mu.Lock()
	l.cfg.Languages = langs
	l.mu.Unlock()
}

// languages are the ones recognition may choose from; nil for any, or when
// [listen] language fixes one for the whole recognizer.
func (l *Listener) languages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cfg.Language != "" {
		return nil
	}
	var out []string
	for _, s := range l.cfg.Languages {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// recognize transcribes pcm (16 kHz) in one of langs. Whisper, left alone,
// picks among a hundred languages and takes a short Polish sentence for
// Armenian; told the user's one language it cannot. With several, what it
// detects stands when it is one of them; otherwise it recognizes again in
// each, keeping the first text that reads as that language. Models that
// cannot be told (Parakeet) and an empty langs recognize as before.
func recognize(rec *sherpa.Recognizer, pcm []float32, langs []string) (string, string, error) {
	if len(langs) == 0 || !rec.CanSetLanguage() {
		return rec.Transcribe(pcm, rate)
	}
	if len(langs) == 1 {
		text, _, err := rec.TranscribeIn(pcm, rate, langs[0])
		return text, langs[0], err
	}
	text, lang, err := rec.Transcribe(pcm, rate)
	if err != nil || slices.Contains(langs, lang) {
		return text, lang, err
	}
	first, firstLang := "", ""
	for _, try := range langs {
		t, _, err := rec.TranscribeIn(pcm, rate, try)
		if err != nil {
			return "", "", err
		}
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		if first == "" {
			first, firstLang = t, try
		}
		if readsAs(t, try) {
			return t, try, nil
		}
	}
	return first, firstLang, nil
}

// readsAs: whether text looks written in lang, for the languages voice can
// tell apart by text (Polish and English); others count as yes.
func readsAs(text, lang string) bool {
	switch lang {
	case "pl", "en":
		return core.DetectLang(text) == lang
	}
	return true
}

// pad surrounds speech with silence: transducers drop the first word when
// audio starts right at the first syllable.
func pad(pcm []float32) []float32 {
	edge := make([]float32, rate/2)
	out := make([]float32, 0, len(pcm)+2*len(edge))
	return append(append(append(out, edge...), pcm...), edge...)
}

func (l *Listener) TranscribeFile(path string) (string, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	pcm, r, err := audio.DecodeWAV(b)
	if err != nil {
		return "", "", err
	}
	return l.Transcribe(pcm, r)
}

// WaitForSpeech returns true once the user has been talking for at least
// min, or false when ctx ends. It records without transcribing; barge-in
// uses it to cut voice off.
func (l *Listener) WaitForSpeech(ctx context.Context, min time.Duration) (bool, error) {
	vad, err := sherpa.NewVAD(filepath.Join(engine.Dirs.Models, "silero-vad", "silero_vad.onnx"), 0.3)
	if err != nil {
		return false, err
	}
	defer vad.Close()
	window := time.Duration(sherpa.VADWindow) * time.Second / rate
	mic, err := audio.OpenCapture(l.cfg.InputDevice, rate, window)
	if err != nil {
		return false, err
	}
	defer mic.Close()
	var heard time.Duration
	for {
		chunk, err := mic.Read(ctx)
		if err != nil {
			return false, nil // ctx ended
		}
		if len(chunk) < sherpa.VADWindow {
			continue
		}
		vad.Accept(chunk[:sherpa.VADWindow])
		vad.Segments()
		if vad.Speaking() {
			heard += window
			if heard >= min {
				return true, nil
			}
		} else {
			heard = 0
		}
	}
}

// Record captures a clip for voice cloning: three short ticks count down,
// the listen chime opens the microphone for seconds, the heard chime ends it.
// Closing stop ends it early with what was recorded so far. It returns raw
// audio at rate, untrimmed; other voice processes stay silent.
func (l *Listener) Record(ctx context.Context, seconds float64, rate int, stop <-chan struct{}) ([]float32, error) {
	defer l.hold()()
	release, err := core.HoldPlayback(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	out := &audio.Output{Device: l.device}
	defer out.Close()
	for i := 0; i < 3; i++ {
		out.Play(ctx, audio.Chime("soft", audio.DeviceRate))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
	out.Play(ctx, audio.Chime("listen", audio.DeviceRate))
	mic, err := audio.OpenCapture(l.cfg.InputDevice, rate, 100*time.Millisecond)
	if err != nil {
		return nil, err
	}
	want := int(seconds * float64(rate))
	pcm := make([]float32, 0, want)
record:
	for len(pcm) < want {
		chunk, err := mic.Read(ctx)
		if err != nil {
			mic.Close()
			return nil, err
		}
		pcm = append(pcm, chunk...)
		select {
		case <-stop:
			break record
		default:
		}
	}
	mic.Close()
	out.Play(ctx, audio.Chime("heard", audio.DeviceRate))
	return pcm[:min(len(pcm), want)], nil
}

type Options struct {
	Wait       time.Duration   // give up if no speech starts within this
	MaxSpeech  time.Duration   // stop recording after this much speech
	EndSilence float64         // seconds of silence that end it; 0: [listen] end_silence
	Stop       <-chan struct{} // closed: end now and transcribe what was said (dictation's second press)
	Level      func(float64)   // microphone level 0..1 while recording, for a visual
	Recorded   func()          // the microphone closed; transcription starts
	// Partial gets the words heard so far while the user still speaks: each
	// phrase (speech up to a short pause) is transcribed as it ends. The
	// final Result comes from the whole recording and may differ.
	Partial func(text string)
}

type Result struct {
	Text    string  `json:"text"`
	Lang    string  `json:"lang,omitempty"`
	Seconds float64 `json:"seconds"`
	Reason  string  `json:"reason,omitempty"` // why no text came back
}

// Listen opens the microphone until the user has spoken and paused, then
// transcribes. Other voice processes stay silent meanwhile, so no agent talks
// into the recording.
func (l *Listener) Listen(ctx context.Context, opts Options) (Result, error) {
	if opts.Wait <= 0 {
		opts.Wait = 15 * time.Second
	}
	if opts.MaxSpeech <= 0 {
		opts.MaxSpeech = 30 * time.Second
	}
	defer l.hold()()
	end := 1.2
	if l.cfg.EndSilence != nil {
		end = *l.cfg.EndSilence
	}
	if opts.EndSilence > 0 {
		end = opts.EndSilence
	}
	rec, err := l.recognizer() // load before the user is invited to speak
	if err != nil {
		return Result{}, err
	}
	vad, err := sherpa.NewVAD(filepath.Join(engine.Dirs.Models, "silero-vad", "silero_vad.onnx"), float32(end))
	if err != nil {
		return Result{}, err
	}
	defer vad.Close()

	release, err := core.HoldPlayback(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	out := &audio.Output{Device: l.device}
	defer out.Close()
	out.Play(ctx, audio.Chime("listen", audio.DeviceRate))

	mic, err := audio.OpenCapture(l.cfg.InputDevice, rate, time.Duration(sherpa.VADWindow)*time.Second/rate)
	if err != nil {
		return Result{}, err
	}
	var segments [][]float32
	// Phrases for Partial: a second VAD that closes a segment at a short pause.
	var phrases *phraser
	if opts.Partial != nil {
		if pv, err := sherpa.NewVAD(filepath.Join(engine.Dirs.Models, "silero-vad", "silero_vad.onnx"), 0.4); err == nil {
			phrases = newPhraser(pv, rec, l.languages(), opts.Partial)
			defer phrases.close()
		}
	}
	started := false
	var speech time.Duration
	quietSince := time.Time{}
	waitUntil := time.Now().Add(opts.Wait)
	window := time.Duration(sherpa.VADWindow) * time.Second / rate
	for {
		chunk, err := mic.Read(ctx)
		if err != nil {
			mic.Close()
			return Result{}, err
		}
		if len(chunk) < sherpa.VADWindow {
			continue
		}
		if opts.Level != nil {
			var sum float64
			for _, s := range chunk {
				sum += float64(s) * float64(s)
			}
			// Speech RMS is roughly 0.02-0.2; the square root spreads it over 0..1.
			opts.Level(min(1, 2.2*math.Sqrt(math.Sqrt(sum/float64(len(chunk))))))
		}
		vad.Accept(chunk[:sherpa.VADWindow])
		segments = append(segments, vad.Segments()...)
		if phrases != nil {
			phrases.accept(chunk[:sherpa.VADWindow])
		}
		now := time.Now()
		if vad.Speaking() {
			started = true
			speech += window
			quietSince = time.Time{}
		} else if started && quietSince.IsZero() {
			quietSince = now
		}
		if !started && now.After(waitUntil) {
			mic.Close()
			out.Play(ctx, audio.Chime("heard", audio.DeviceRate))
			return Result{Reason: "no speech"}, nil
		}
		// The VAD closes a segment after end seconds of silence; stop once
		// it has and nothing new began.
		if started && len(segments) > 0 && !quietSince.IsZero() && now.Sub(quietSince) >= time.Duration(end*float64(time.Second)) {
			break
		}
		if speech >= opts.MaxSpeech {
			break
		}
		select {
		case <-opts.Stop:
			if !started {
				mic.Close()
				out.Play(ctx, audio.Chime("heard", audio.DeviceRate))
				return Result{Reason: "no speech"}, nil
			}
		default:
			continue
		}
		break
	}
	mic.Close()
	if phrases != nil {
		phrases.close() // the final transcription follows; waits for a phrase in progress
	}
	if opts.Recorded != nil {
		opts.Recorded()
	}
	vad.Flush()
	segments = append(segments, vad.Segments()...)
	out.Play(ctx, audio.Chime("heard", audio.DeviceRate))
	release()

	var pcm []float32
	gap := make([]float32, rate/5)
	for i, s := range segments {
		if i > 0 {
			pcm = append(pcm, gap...)
		}
		pcm = append(pcm, s...)
	}
	res := Result{Seconds: float64(len(pcm)) / rate}
	if len(pcm) == 0 {
		res.Reason = "no speech"
		return res, nil
	}
	recMu.Lock()
	text, lang, err := recognize(rec, pad(pcm), l.languages())
	recMu.Unlock()
	if err != nil {
		return Result{}, err
	}
	res.Text, res.Lang = strings.TrimSpace(text), lang
	if res.Text == "" {
		res.Reason = "speech not understood"
	}
	return res, nil
}

// recMu keeps the recognizer to one transcription at a time.
var recMu sync.Mutex

// phraser shows a recording's words while the user still speaks. A phrase
// (speech up to a short pause) is transcribed for good when it ends; the
// phrase in progress is transcribed again every tentativeEvery, so words
// appear without waiting for a pause. One transcription runs at a time, in
// the background; the microphone never waits for it.
type phraser struct {
	vad     *sherpa.VAD
	jobs    chan phraseJob
	done    chan struct{}
	stopped atomic.Bool
	busy    atomic.Bool // a transcription is running or queued

	pre, cur []float32 // recent audio before speech; the phrase in progress
	last     time.Time // when the phrase in progress was last sent
}

type phraseJob struct {
	pcm   []float32
	final bool // a finished phrase, rather than the one in progress
}

const (
	tentativeEvery = 700 * time.Millisecond
	preRoll        = rate * 3 / 10 // audio kept from before the VAD hears speech
)

func newPhraser(vad *sherpa.VAD, rec *sherpa.Recognizer, langs []string, report func(string)) *phraser {
	p := &phraser{vad: vad, jobs: make(chan phraseJob, 64), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		var said []string
		for job := range p.jobs {
			if p.stopped.Load() {
				continue // recording ended: the final transcription takes over
			}
			recMu.Lock()
			text, _, err := recognize(rec, pad(job.pcm), langs)
			recMu.Unlock()
			if len(p.jobs) == 0 {
				p.busy.Store(false)
			}
			text = strings.TrimSpace(text)
			if err != nil {
				continue
			}
			shown := said
			if job.final {
				if text != "" {
					said = append(said, text)
				}
				shown = said
			} else if text != "" {
				shown = append(append([]string(nil), said...), text)
			}
			if len(shown) > 0 {
				report(strings.Join(shown, " "))
			}
		}
	}()
	return p
}

func (p *phraser) accept(window []float32) {
	p.vad.Accept(window)
	speaking := p.vad.Speaking()
	if speaking && len(p.cur) == 0 {
		p.cur = append(p.cur, p.pre...) // the start of the first word
		p.last = time.Now()
	}
	if len(p.cur) > 0 {
		p.cur = append(p.cur, window...)
	} else {
		p.pre = append(p.pre, window...)
		if over := len(p.pre) - preRoll; over > 0 {
			p.pre = append(p.pre[:0], p.pre[over:]...)
		}
	}
	if segs := p.vad.Segments(); len(segs) > 0 {
		for _, seg := range segs {
			p.send(phraseJob{pcm: seg, final: true})
		}
		p.cur, p.pre = nil, p.pre[:0]
		return
	}
	// The phrase in progress, when nothing else waits for the recognizer.
	if speaking && time.Since(p.last) >= tentativeEvery && !p.busy.Load() {
		p.last = time.Now()
		p.send(phraseJob{pcm: append([]float32(nil), p.cur...)})
	}
}

func (p *phraser) send(job phraseJob) {
	p.busy.Store(true)
	select {
	case p.jobs <- job:
	default: // far behind: skip it rather than hold up the microphone
	}
}

// close stops taking phrases, waits for the one being transcribed and frees
// the VAD; it may be called twice.
func (p *phraser) close() {
	if p.stopped.Swap(true) {
		return
	}
	close(p.jobs)
	<-p.done
	p.vad.Close()
}
