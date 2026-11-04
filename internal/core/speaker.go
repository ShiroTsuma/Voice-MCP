// Package core turns speak requests into audio: cleanup, queueing, engine
// fallback, sentence pipelining and the cross-process playback lock.
package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/engine"
	"voice/internal/preset"
)

const (
	IntentInfo      = "info"
	IntentAttention = "attention"
	IntentQuestion  = "question"

	// minSentence merges short fragments so engines are not called per word.
	minSentence = 24
)

var (
	ErrInterrupted = errors.New("interrupted")
	ErrStopped     = errors.New("stopped")
	ErrMuted       = errors.New("muted")
)

type Request struct {
	Text      string
	Intent    string // info | attention | question
	Lang      string // "pl", "en", ...; "auto" detects; empty uses default_lang
	Preset    string // empty: active preset
	Interrupt bool   // stop the current utterance and drop the queue
	Cache     bool   // reuse rendered audio for repeated phrases (hooks)
	// SaveTo renders into this file (.wav, or .mp3 with ffmpeg) instead of
	// the speakers: the same voice, nothing played, and mute does not stop it.
	SaveTo string
	// Lines, when given, replace Text: a script, each line in its own preset
	// (empty: Preset), spoken as one utterance that keeps flowing.
	Lines []ScriptLine
	// MaxChars limits each line (or Text); 0 uses max_chars from the config.
	MaxChars int
}

// ScriptLine is one line of a script and the preset that says it. Pause is
// silence after it in seconds (0: a short breath when the voice changes); a
// line with a pause and no text is just a break.
type ScriptLine struct {
	Text   string
	Preset string
	Pause  float64
}

// maxPause caps one break in a script, in seconds.
const maxPause = 10

type Result struct {
	Engine  string  // engine that produced the audio
	Seconds float64 // SaveTo: how long the saved audio is
	Err     error
}

type Ticket struct {
	ID       string
	Position int    // 0: plays next
	Note     string // the default voice speaks because the preset's voice is gone
	Done     <-chan Result
}

type item struct {
	id     string
	text   string
	intent string
	lang   string
	preset preset.Resolved
	cache  bool
	saveTo string
	parts  []part  // the text by voice: one part, or a script's lines
	lead   float64 // silence before the first part, in seconds
	done   chan Result

	line Line // progress, under Speaker.mu
}

// part is a stretch of an utterance in one voice; pause is the silence after
// it, in seconds (0: lineGap when the next voice differs).
type part struct {
	text   string
	preset preset.Resolved
	pause  float64
}

type Speaker struct {
	cfg *preset.Config
	out audio.Output

	mu       sync.Mutex
	engines  map[string]engine.Engine
	loading  map[string]*sync.Mutex // per-engine creation locks
	used     map[string]time.Time   // last synthesis per engine, for idle unloading
	activity func(bool)
	// preparingFn learns when an utterance is rendering before it is heard.
	preparingFn func(bool)
	audibleOn   bool // activity(true) was reported for the current stretch of speech (loop only)
	queue       []*item
	current     *item
	cancel      context.CancelFunc
	active      string
	muted       bool
	nextID      int
	lastErr     string
	gone        map[string]bool // presets whose missing voice was already reported
	// The sentence playing: its loudness every levelStep, from levelAt.
	levels  []float32
	levelAt time.Time
	wake    chan struct{}
	closed  chan struct{}
}

// OnPreparing sets a callback for when an utterance starts rendering (true)
// and when its first sound plays or it ends (false): a slow voice takes a
// while before anything is heard. Call before speaking.
func (s *Speaker) OnPreparing(fn func(preparing bool)) { s.preparingFn = fn }

// OnActivity sets a callback for when speech starts (true) and when the
// queue has drained (false), e.g. to duck other apps. Call before speaking.
func (s *Speaker) OnActivity(fn func(speaking bool)) { s.activity = fn }

// OpenOutput opens the audio device right away instead of on first speech.
func (s *Speaker) OpenOutput() error {
	return s.out.Play(context.Background(), audio.Silence(audio.DeviceRate, 0.05))
}

// RecordTo makes the speaker write everything to a WAV file on Close instead
// of playing it. Call before the first Speak.
func (s *Speaker) RecordTo(path string) { s.out.Record = path }

func NewSpeaker(cfg *preset.Config) *Speaker {
	s := &Speaker{
		cfg:     cfg,
		engines: map[string]engine.Engine{},
		loading: map[string]*sync.Mutex{},
		used:    map[string]time.Time{},
		active:  cfg.DefaultPreset,
		wake:    make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	s.out.Device = cfg.Device
	go s.loop()
	return s
}

// Speak validates and queues an utterance; it returns before playback.
func (s *Speaker) Speak(req Request) (Ticket, error) {
	intent := req.Intent
	if intent == "" {
		intent = IntentInfo
	}
	if intent != IntentInfo && intent != IntentAttention && intent != IntentQuestion {
		return Ticket{}, fmt.Errorf("intent must be info, attention or question, got %q", req.Intent)
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = s.cfg.MaxChars
	}
	lines := req.Lines
	if len(lines) == 0 {
		lines = []ScriptLine{{Text: req.Text}}
	}
	var texts []string
	var kept []ScriptLine
	lead := 0.0 // a break before the first line
	for _, l := range lines {
		pause := min(max(l.Pause, 0), maxPause)
		t := Truncate(Clean(l.Text), maxChars)
		if strings.TrimSpace(t) == "" {
			// A break alone: after the line before it, or before the first.
			if k := len(kept) - 1; k >= 0 {
				kept[k].Pause = min(kept[k].Pause+pause, maxPause)
			} else {
				lead = min(lead+pause, maxPause)
			}
			continue
		}
		texts = append(texts, t)
		kept = append(kept, ScriptLine{Text: t, Preset: l.Preset, Pause: pause})
	}
	if len(kept) == 0 {
		return Ticket{}, errors.New("nothing speakable left after removing markdown, code and links")
	}
	text := strings.Join(texts, " ")
	lang := strings.ToLower(req.Lang)
	if lang == "" {
		lang = strings.ToLower(s.cfg.DefaultLang)
	}
	if lang == "" || lang == "auto" {
		lang = DetectLang(text)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.muted || SharedMuted()) && req.SaveTo == "" {
		return Ticket{}, ErrMuted
	}
	var parts []part
	note := ""
	for _, l := range kept {
		name := l.Preset
		if name == "" {
			name = req.Preset
		}
		if name == "" {
			name = s.active
		}
		p, err := s.cfg.Resolve(name)
		if err != nil {
			return Ticket{}, err
		}
		// A removed cloned voice falls back to the default preset; other voice
		// errors are reported now rather than at playback.
		gone := p.Name
		p, n, err := voiceOrDefault(s.cfg, p, lang)
		if err != nil {
			return Ticket{}, err
		}
		if n != "" && !s.gone[gone] {
			if s.gone == nil {
				s.gone = map[string]bool{}
			}
			s.gone[gone] = true // said once; the user knows
			if note == "" {
				note = n
			}
		}
		// Lines in the same voice in a row are one part: they render together,
		// unless a break was asked for between them.
		if k := len(parts) - 1; k >= 0 && parts[k].preset.Name == p.Name && parts[k].pause == 0 {
			parts[k].text += " " + l.Text
			parts[k].pause = l.Pause
			continue
		}
		parts = append(parts, part{text: l.Text, preset: p, pause: l.Pause})
	}
	s.nextID++
	it := &item{id: "v" + strconv.Itoa(s.nextID), text: text, intent: intent, lang: lang, preset: parts[0].preset, parts: parts, lead: lead,
		cache: req.Cache && req.SaveTo == "" && len(parts) == 1, saveTo: req.SaveTo, done: make(chan Result, 1)}
	if req.Interrupt {
		s.dropLocked(ErrInterrupted)
	}
	s.queue = append(s.queue, it)
	pos := len(s.queue) - 1
	if s.current != nil {
		pos++
	}
	s.signal()
	return Ticket{ID: it.id, Position: pos, Done: it.done, Note: note}, nil
}

// Stop silences the current utterance and clears the queue.
func (s *Speaker) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLocked(ErrStopped)
}

func (s *Speaker) dropLocked(reason error) {
	for _, it := range s.queue {
		it.done <- Result{Err: reason}
	}
	s.queue = nil
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Speaker) SetMuted(m bool) {
	s.mu.Lock()
	s.muted = m
	if m {
		s.dropLocked(ErrMuted)
	}
	s.mu.Unlock()
	updateState(func(st *sharedState) { st.Muted = m })
}

func (s *Speaker) SetPreset(name string) error {
	if _, err := s.cfg.Resolve(name); err != nil {
		return err
	}
	s.mu.Lock()
	s.active = name
	s.mu.Unlock()
	return nil
}

type Status struct {
	Preset    string
	Muted     bool
	Speaking  bool
	Queued    int
	LastError string
	// Line is what is being said now; nil while voice is quiet.
	Line *Line
}

// Line is the utterance being spoken and how far it got: the runes before
// Done are said; the sentence [From, To) plays since At for Seconds.
type Line struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Done    int       `json:"done"`
	From    int       `json:"from"`
	To      int       `json:"to"`
	At      time.Time `json:"at"`
	Seconds float64   `json:"seconds"`
	// Preparing: nothing of it plays yet; its voice is still rendering.
	Preparing bool `json:"preparing"`
}

func (s *Speaker) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Preset: s.active, Muted: s.muted || SharedMuted(), Speaking: s.current != nil, Queued: len(s.queue), LastError: s.lastErr}
	if s.current != nil {
		l := s.current.line
		l.ID, l.Text = s.current.id, s.current.text
		l.Preparing = l.At.IsZero()
		st.Line = &l
	}
	return st
}

// playing records that the runes [from, to) of it's text start now and last
// pcm's length; everything before from is said.
func (s *Speaker) playing(it *item, from, to int, pcm []float32) {
	levels := envelope(pcm)
	s.mu.Lock()
	it.line.Done, it.line.From, it.line.To = from, from, to
	it.line.At, it.line.Seconds = time.Now(), float64(len(pcm))/float64(audio.DeviceRate)
	s.levels, s.levelAt = levels, it.line.At
	s.mu.Unlock()
}

const levelStep = 40 * time.Millisecond

// envelope is pcm's loudness every levelStep, 0..1.
func envelope(pcm []float32) []float32 {
	n := int(float64(audio.DeviceRate) * levelStep.Seconds())
	out := make([]float32, 0, len(pcm)/n+1)
	for i := 0; i < len(pcm); i += n {
		end := min(i+n, len(pcm))
		var sum float64
		for _, v := range pcm[i:end] {
			sum += float64(v) * float64(v)
		}
		rms := math.Sqrt(sum / float64(end-i))
		out = append(out, float32(min(1, rms*4))) // speech peaks near 0.25 rms
	}
	return out
}

// Level is how loud the voice is now, 0..1, for an animation; 0 when quiet.
func (s *Speaker) Level() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil || len(s.levels) == 0 {
		return 0
	}
	i := int(time.Since(s.levelAt) / levelStep)
	if i < 0 || i >= len(s.levels) {
		return 0
	}
	return float64(s.levels[i])
}

// Warm loads the active preset's engine for the default language, so the
// first utterance does not wait for a model to load.
func (s *Speaker) Warm() {
	s.mu.Lock()
	name := s.active
	s.mu.Unlock()
	p, err := s.cfg.Resolve(name)
	if err != nil {
		return
	}
	lang := strings.ToLower(s.cfg.DefaultLang)
	if lang == "" || lang == "auto" {
		lang = "en"
	}
	s.Engine(p.For(lang).Engines[0])
}

// WarmVoices loads, in the background, the engines the given presets speak
// with, starting the servers of GPU voices so their first sentence is not
// late. Presets that do not resolve are skipped.
func (s *Speaker) WarmVoices(presets []string) {
	lang := strings.ToLower(s.cfg.DefaultLang)
	if lang == "" || lang == "auto" {
		lang = "en"
	}
	seen := map[string]bool{}
	for _, name := range presets {
		p, err := s.cfg.Resolve(name)
		if err != nil {
			continue
		}
		engineName := p.For(lang).Engines[0]
		if seen[engineName] {
			continue
		}
		seen[engineName] = true
		go func() {
			e, err := s.Engine(engineName)
			if err != nil {
				return
			}
			if w, ok := e.(engine.Warmer); ok {
				if err := w.Warm(context.Background()); err != nil && Logf != nil {
					Logf("voices: %s did not load: %v", engineName, err)
				}
			}
		}()
	}
}

// Engine returns a started engine, creating it on first use. Failed starts
// are retried on the next call so a sidecar that comes up later is picked up.
// Loading a model takes seconds, so it holds only that engine's lock, never
// the speaker's: Speak and Status stay instant meanwhile.
func (s *Speaker) Engine(name string) (engine.Engine, error) {
	s.mu.Lock()
	if e := s.engines[name]; e != nil {
		s.mu.Unlock()
		return e, nil
	}
	l := s.loading[name]
	if l == nil {
		l = &sync.Mutex{}
		s.loading[name] = l
	}
	s.mu.Unlock()

	l.Lock()
	defer l.Unlock()
	s.mu.Lock()
	e := s.engines[name]
	s.mu.Unlock()
	if e != nil {
		return e, nil
	}
	e, err := s.newEngine(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.engines[name] = e
	s.used[name] = time.Now()
	s.mu.Unlock()
	return e, nil
}

func (s *Speaker) newEngine(name string) (engine.Engine, error) {
	cfg, ok := s.cfg.Engines[name]
	if !ok {
		return nil, fmt.Errorf("unknown engine %q", name)
	}
	if mixes := s.cfg.MixesFor(name); len(mixes) > 0 {
		withMixes := engine.Config{engine.MixesKey: mixes}
		for k, v := range cfg {
			withMixes[k] = v
		}
		cfg = withMixes
	}
	return engine.New(name, cfg)
}

// UnloadIdle closes engines unused for longer than after, freeing their
// models; the next utterance loads them again. Nothing is unloaded while
// speech is playing or queued, since rendering holds engines without locks.
func (s *Speaker) UnloadIdle(after time.Duration) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil || len(s.queue) > 0 {
		return nil
	}
	var closed []string
	for name, e := range s.engines {
		if last, ok := s.used[name]; ok && time.Since(last) < after {
			continue
		}
		if l := s.loading[name]; l != nil && !l.TryLock() {
			continue // being created right now
		} else if l != nil {
			l.Unlock()
		}
		e.Close()
		delete(s.engines, name)
		delete(s.used, name)
		closed = append(closed, name)
	}
	return closed
}

func (s *Speaker) Close() {
	s.Stop()
	select {
	case <-s.closed:
		return
	default:
		close(s.closed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.engines {
		e.Close()
	}
	if err := s.out.Close(); err != nil {
		s.lastErr = err.Error()
	}
}

// Err reports the last playback or output error.
func (s *Speaker) Err() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *Speaker) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Speaker) loop() {
	for {
		s.mu.Lock()
		var it *item
		if len(s.queue) > 0 {
			it, s.queue = s.queue[0], s.queue[1:]
		}
		var ctx context.Context
		if it != nil {
			ctx, s.cancel = context.WithCancel(context.Background())
			s.current = it
		}
		s.mu.Unlock()

		if it == nil {
			select {
			case <-s.wake:
				continue
			case <-s.closed:
				return
			}
		}

		res := s.play(ctx, it) // calls audible before its first sound
		s.mu.Lock()
		s.cancel()
		s.current, s.cancel = nil, nil
		if res.Err != nil && !errors.Is(res.Err, context.Canceled) {
			s.lastErr = res.Err.Error()
		}
		idle := len(s.queue) == 0
		s.mu.Unlock()
		if hook := s.activity; hook != nil && idle && s.audibleOn {
			hook(false)
		}
		if idle {
			s.audibleOn = false
		}
		if errors.Is(res.Err, context.Canceled) {
			res.Err = ErrInterrupted
		}
		it.done <- res
	}
}

// audible reports the start of sound to OnActivity once per stretch of speech:
// other apps are ducked (and barge-in listens) when voice is about to be heard,
// not while the first sentence is still being synthesized. Runs on the loop.
func (s *Speaker) audible() {
	if s.audibleOn {
		return
	}
	s.audibleOn = true
	if hook := s.activity; hook != nil {
		hook(true)
	}
}

type rendered struct {
	pcm []float32
	err error
	gap bool // a pause or an effect's tail between parts: no sentence of its own
}

func (s *Speaker) play(ctx context.Context, it *item) Result {
	// Saving to a file plays nothing: no playback lock, chime or popup.
	saving := it.saveTo != ""
	var saved []float32
	if !saving {
		release, err := acquirePlayback(ctx)
		if err != nil {
			return Result{Err: err}
		}
		defer release()
	}
	// Rendering before anything is heard: the screen can say so.
	ready := func() {}
	if fn := s.preparingFn; fn != nil && !saving {
		fn(true)
		once := sync.Once{}
		ready = func() { once.Do(func() { fn(false) }) }
		defer ready()
	}

	// Each part (one, or a script's lines by voice) with what renders it.
	type voiced struct {
		part
		choice    preset.Choice
		effects   audio.Chain
		pitch     float64
		sentences []string
	}
	var vs []voiced
	var sentences []string // all of them in order, for Status
	for _, p := range it.parts {
		v := voiced{part: p, choice: p.preset.For(it.lang)}
		var err error
		if v.choice.Voice, err = resolveVoice(v.choice.Voice); err != nil {
			return Result{Err: err}
		}
		// Effects keep state across sentences; Resolve already validated them.
		if v.effects, err = audio.ParseChain(p.preset.DSP, audio.DeviceRate); err != nil {
			return Result{Err: err}
		}
		// Pitch is shifted by playing the audio at a different rate. The engine
		// speaks faster or slower by the same factor, so tempo stays as set.
		v.pitch = math.Pow(2, v.choice.Pitch/12)
		v.sentences = Split(p.text, minSentence)
		sentences = append(sentences, v.sentences...)
		vs = append(vs, v)
	}
	first := vs[0]

	// A question or an alert opens with a chime, played right before the
	// first sound is ready: with a GPU voice that renders for a while, a
	// chime first would be followed by silence.
	chimed := it.intent == IntentInfo || saving
	chime := func() error {
		if chimed {
			return nil
		}
		chimed = true
		ready()
		s.audible()
		return s.out.Play(ctx, audio.Chime(it.preset.Chime, audio.DeviceRate))
	}

	// Cached phrases skip synthesis entirely, so hook notifications play at
	// once even when the model is not loaded in this process.
	key := ""
	if it.cache {
		key = cacheKey(it.preset, first.choice, it.lang, it.text)
		if pcm := readCache(key); pcm != nil {
			if err := chime(); err != nil {
				return Result{Err: err}
			}
			ready()
			s.audible()
			s.playing(it, 0, len([]rune(it.text)), pcm)
			if err := s.out.Play(ctx, pcm); err != nil {
				return Result{Engine: "cache", Err: err}
			}
			_ = s.out.Play(ctx, audio.Silence(audio.DeviceRate, 0.12))
			markSpoke()
			return Result{Engine: "cache"}
		}
	}
	var heard []float32 // everything played, kept only for the cache

	// Render ahead while earlier sentences play. The queue holds a whole
	// batch, so the batch after it starts rendering as soon as this one is
	// ready and has all of its playing time to get done; across a script's
	// lines too, so the next voice is ready when its turn comes.
	ch := make(chan rendered, batchSize)
	var used string
	go func() {
		defer close(ch)
		put := func(r rendered) bool {
			select {
			case ch <- r:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if it.lead > 0 && !put(rendered{pcm: audio.Silence(audio.DeviceRate, it.lead), gap: true}) {
			return
		}
		for n, v := range vs {
			// Between parts: the break the script asked for, else a short breath
			// when the voice changes (a turn in the dialogue).
			if n > 0 {
				gap := vs[n-1].pause
				if gap == 0 && vs[n-1].preset.Name != v.preset.Name {
					gap = lineGap
				}
				if gap > 0 && !put(rendered{pcm: audio.Silence(audio.DeviceRate, gap), gap: true}) {
					return
				}
			}
			engines := v.choice.Engines
			send := func(pcm []float32) bool {
				v.effects.Process(pcm)
				audio.Gain(pcm, v.preset.Volume)
				return put(rendered{pcm: pcm})
			}
			for k := 0; k < len(v.sentences); {
				// An engine slower than its own speech (a GPU model) renders a few
				// sentences at once, in about the time of one, from the first
				// sentence on: speech starts a moment later and then flows, rather
				// than a short sentence and a long silence. The next batch renders
				// while this one plays. A batch that fails goes sentence by sentence.
				if m := min(batchSize, len(v.sentences)-k); m > 1 && engines[0] == v.choice.Engines[0] {
					if pcms, err := s.renderBatch(ctx, engines[0], v.pitch, it.lang, v.choice, v.sentences[k:k+m]); err == nil {
						used = engines[0]
						for _, pcm := range pcms {
							if !send(pcm) {
								return
							}
						}
						k += m
						continue
					}
				}
				req := engine.Request{Text: v.sentences[k], Lang: it.lang, Voice: v.choice.Voice, Speed: v.choice.Speed / v.pitch}
				pcm, name, rest, err := s.render(ctx, engines, v.choice.Engines[0], v.pitch, req)
				if err != nil {
					put(rendered{err: err})
					return
				}
				// Stay on the engine that worked so the voice does not change
				// mid-line; later fallbacks remain for failures.
				engines, used = rest, name
				if !send(pcm) {
					return
				}
				k++
			}
			// The line's effects ring out before the next voice.
			if tail := v.effects.Tail(); len(tail) > 0 {
				audio.Gain(tail, v.preset.Volume)
				if !put(rendered{pcm: tail, gap: true}) {
					return
				}
			}
		}
	}()

	// Where each sentence sits in the text, for Status.
	runes, at, i := []rune(it.text), 0, 0
	for r := range ch {
		if r.err != nil {
			return Result{Engine: used, Err: r.err}
		}
		if saving {
			saved = append(saved, r.pcm...)
			continue
		}
		if err := chime(); err != nil {
			return Result{Engine: used, Err: err}
		}
		ready()
		s.audible()
		if !r.gap {
			from, to := at, len(runes)
			if i < len(sentences) {
				if k := strings.Index(string(runes[at:]), sentences[i]); k >= 0 {
					from = at + len([]rune(string(runes[at:])[:k]))
					to = from + len([]rune(sentences[i]))
				}
				i++
			}
			at = to
			s.playing(it, from, to, r.pcm)
		}
		if err := s.out.Play(ctx, r.pcm); err != nil {
			return Result{Engine: used, Err: err}
		}
		if key != "" {
			heard = append(heard, r.pcm...)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{Engine: used, Err: err}
	}
	if saving {
		if err := audio.WriteFile(it.saveTo, saved, audio.DeviceRate); err != nil {
			return Result{Engine: used, Err: err}
		}
		return Result{Engine: used, Seconds: float64(len(saved)) / audio.DeviceRate}
	}
	// Let the device drain its buffer before the lock goes to someone else.
	_ = s.out.Play(ctx, audio.Silence(audio.DeviceRate, 0.12))
	// A fallback rendition is not cached, or it would outlive the outage.
	if key != "" && used == first.choice.Engines[0] {
		writeCache(key, heard)
	}
	markSpoke()
	return Result{Engine: used}
}

// lineGap is the breath between two voices of a script, in seconds.
const lineGap = 0.3

// Logf, when set, records what the log should know: an engine that failed
// while a fallback spoke.
var Logf func(format string, args ...any)

// batchSize is how many sentences an engine that renders batches gets at once.
const batchSize = 4

// renderBatch renders sentences with one call to an engine that takes
// batches (engine.Batcher), at the device rate; an error when it cannot.
func (s *Speaker) renderBatch(ctx context.Context, name string, pitch float64, lang string, choice preset.Choice, sentences []string) ([][]float32, error) {
	e, err := s.Engine(name)
	if err != nil {
		return nil, err
	}
	b, ok := e.(engine.Batcher)
	if !ok {
		return nil, errors.New("no batches")
	}
	s.mu.Lock()
	s.used[name] = time.Now()
	s.mu.Unlock()
	reqs := make([]engine.Request, len(sentences))
	for i, t := range sentences {
		reqs[i] = engine.Request{Text: t, Lang: lang, Voice: choice.Voice, Speed: choice.Speed / pitch}
	}
	chunks, err := b.SynthesizeBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(chunks))
	for i, c := range chunks {
		if len(c.PCM) == 0 {
			return nil, errors.New("no audio")
		}
		out[i] = audio.Resample(c.PCM, int(math.Round(float64(c.SampleRate)*pitch)), audio.DeviceRate)
	}
	return out, nil
}

// render tries engines in order and returns audio at the device rate, the
// engine used and the engines still eligible for the following sentences.
func (s *Speaker) render(ctx context.Context, engines []string, primary string, pitch float64, req engine.Request) ([]float32, string, []string, error) {
	var errs []string
	for i, name := range engines {
		e, err := s.Engine(name)
		s.mu.Lock()
		s.used[name] = time.Now()
		s.mu.Unlock()
		if err == nil {
			var pcm []float32
			// A voice name belongs to the primary engine only.
			r := req
			if name != primary {
				r.Voice = engine.Voice{}
			}
			err = e.Synthesize(ctx, r, func(c engine.Chunk) error {
				from := int(math.Round(float64(c.SampleRate) * pitch))
				pcm = append(pcm, audio.Resample(c.PCM, from, audio.DeviceRate)...)
				return nil
			})
			if err == nil && len(pcm) > 0 {
				// Speaking with a fallback: say why the voice wanted did not.
				if i > 0 && Logf != nil {
					Logf("speech: %s spoke instead of %s: %s", name, engines[0], strings.Join(errs, "; "))
				}
				return pcm, name, engines[i:], nil
			}
			if err == nil {
				err = errors.New("no audio")
			}
		}
		if ctx.Err() != nil {
			return nil, "", nil, ctx.Err()
		}
		errs = append(errs, name+": "+err.Error())
	}
	return nil, "", nil, errors.New("all engines failed: " + strings.Join(errs, "; "))
}
