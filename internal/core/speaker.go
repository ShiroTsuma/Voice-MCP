// Package core turns speak requests into audio: cleanup, queueing, engine
// fallback, sentence pipelining and the cross-process playback lock.
package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// Mood is Text's mood (Moods), shown where the engine can.
	Mood string
	// Mode is how a script is heard: "play" renders ahead while it plays,
	// "render" renders all of it first (one engine at a time, so GPU models
	// are not loaded and unloaded line by line) and then plays it, "auto"
	// (or empty) renders first when the script uses two or more GPU engines.
	// SaveTo always renders first.
	Mode string
	// Turn joins this text to the owner's utterance of the same turn while
	// it is queued or playing, as the next piece of one reply; a new turn,
	// or speech without one, closes the owner's open turn. Plain text only.
	Turn string
	// Owner is the session that speaks (its agent's process ID), so one
	// session's speech can be stopped without the others'; 0 is no session.
	Owner uint32
}

// ScriptLine is one line of a script and the preset that says it. Pause is
// silence after it in seconds (0: a short breath when the voice changes); a
// line with a pause and no text is just a break.
type ScriptLine struct {
	Text   string
	Preset string
	Pause  float64
	Mood   string // how it is said, where the engine can show it (Moods)
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
	Position int      // 0: plays next
	Note     string   // the default voice speaks because the preset's voice is gone
	Voices   []string // the presets that will say it, in order
	Done     <-chan Result
	// Appended: the text joined an open turn's utterance (ID is that one's);
	// Done is nil, the utterance's own caller holds it.
	Appended bool
}

// Track is what became of one utterance: queued, rendering (its voice is
// being made, nothing heard yet), playing, then done, or error, stopped,
// interrupted or muted. The speaker keeps the last trackKeep of them.
type Track struct {
	ID       string   `json:"id"`
	State    string   `json:"state"`
	Voices   []string `json:"voices"`             // the presets that say it
	Engine   string   `json:"engine,omitempty"`   // the engine that made the sound
	Fallback bool     `json:"fallback,omitempty"` // a fallback engine, not the voice's own
	Error    string   `json:"error,omitempty"`
	Saved    string   `json:"saved,omitempty"` // save_to: the file
	// Sounds were made by the engine; Dropped it could not make, so they
	// were left out. Mood: applied, or ignored by the engine.
	Sounds  []string `json:"sounds,omitempty"`
	Dropped []string `json:"dropped,omitempty"`
	Mood    string   `json:"mood,omitempty"`
	// Progress says how far rendering a scene first has got (render mode).
	Progress string     `json:"progress,omitempty"`
	Queued   time.Time  `json:"queued"`
	Heard    *time.Time `json:"heard,omitempty"` // the first sound
	Ended    *time.Time `json:"ended,omitempty"`
}

const trackKeep = 100

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

	line  Line   // progress, under Speaker.mu
	track *Track // what became of it, under Speaker.mu
	// primary is the engine its voice asks for (play sets it), to tell a
	// fallback apart.
	primary string
	tags    *tagUse // what became of its tags and moods (play)
	grouped bool    // rendered first, one engine at a time (Request.Mode)
	owner   uint32  // Request.Owner
	// A turn's utterance takes more text while open (Request.Turn): parts
	// and text grow under Speaker.mu, grew wakes play to render them.
	turn string
	open bool
	grew chan struct{}
	// why is the reason it was cut off while playing (stopped, interrupted),
	// set under Speaker.mu before its context is cancelled.
	why error
}

// part is a stretch of an utterance in one voice; pause is the silence after
// it, in seconds (0: lineGap when the next voice differs).
type part struct {
	text   string
	preset preset.Resolved
	pause  float64
	mood   string
	before float64 // silence before it: a break that opened a turn's next piece
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
	tracks      []*Track        // the last utterances, oldest first
	// The sentence playing: its loudness every levelStep, from levelAt.
	levels  []float32
	levelAt time.Time
	wake    chan struct{}
	closed  chan struct{}
	// cutFn learns how many words of an utterance were heard when it ended
	// before its end (stopped, interrupted, muted, failed).
	cutFn func(id string, words int)
}

// OnCut sets a callback for an utterance that ended before all of it was
// heard: its id and how many of its words were heard (0: none). It runs on
// its own goroutine. Call before speaking.
func (s *Speaker) OnCut(fn func(id string, words int)) { s.cutFn = fn }

// endedLocked records how an utterance ended and, when it was cut short,
// tells OnCut how much of it was heard; the caller holds s.mu.
func (s *Speaker) endedLocked(it *item, res Result) {
	endLocked(it, res)
	if res.Err == nil || it.saveTo != "" || s.cutFn == nil {
		return
	}
	words := 0
	if l := it.line; !l.At.IsZero() {
		// The sentence playing [From, To) is heard in proportion to its time.
		f := 1.0
		if l.Seconds > 0 {
			f = min(1, time.Since(l.At).Seconds()/l.Seconds)
		}
		runes := []rune(it.text)
		at := min(len(runes), max(l.Done, l.From+int(f*float64(l.To-l.From))))
		words = len(strings.Fields(string(runes[:at])))
	}
	go s.cutFn(it.id, words)
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
		lines = []ScriptLine{{Text: req.Text, Mood: req.Mood}}
	}
	// {pause} cuts a line in two with silence between.
	var cut []ScriptLine
	for _, l := range lines {
		pieces, pauses := splitPauses(Clean(l.Text))
		for i, piece := range pieces {
			n := l
			n.Text, n.Pause = piece, pauses[i]
			if i == len(pieces)-1 {
				n.Pause += l.Pause
			}
			cut = append(cut, n)
		}
	}
	lines = cut
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
		kept = append(kept, ScriptLine{Text: t, Preset: l.Preset, Pause: pause, Mood: l.Mood})
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
		// unless a break or another mood comes between them.
		if k := len(parts) - 1; k >= 0 && parts[k].preset.Name == p.Name && parts[k].pause == 0 && parts[k].mood == l.Mood {
			parts[k].text += " " + l.Text
			parts[k].pause = l.Pause
			continue
		}
		parts = append(parts, part{text: l.Text, preset: p, pause: l.Pause, mood: l.Mood})
	}
	var voices []string
	for _, p := range parts {
		if !slices.Contains(voices, p.preset.Name) {
			voices = append(voices, p.preset.Name)
		}
	}
	// The next piece of a reply joins the utterance its turn opened, queued or
	// playing: no new queue slot, no break between them. Any other speech of
	// the owner closes its open turn first.
	turn := req.Turn != "" && req.Owner != 0 && req.SaveTo == "" && len(req.Lines) == 0
	if turn && !req.Interrupt {
		if it := s.openTurnLocked(req.Owner); it != nil && it.turn == req.Turn && it.lang == lang {
			parts[0].before = lead
			it.parts = append(it.parts, parts...)
			it.text += " " + text
			for _, v := range voices {
				if !slices.Contains(it.track.Voices, v) {
					it.track.Voices = append(it.track.Voices, v)
				}
			}
			select {
			case it.grew <- struct{}{}:
			default:
			}
			pos := slices.Index(s.queue, it) + 1 // 0 while it plays
			return Ticket{ID: it.id, Position: pos, Note: note, Voices: voices, Appended: true}, nil
		}
	}
	s.endTurnLocked(req.Owner)
	s.nextID++
	it := &item{id: "v" + strconv.Itoa(s.nextID), text: text, intent: intent, lang: lang, preset: parts[0].preset, parts: parts, lead: lead, grouped: s.renderFirst(req.Mode, req.SaveTo, parts),
		cache: req.Cache && req.SaveTo == "" && len(parts) == 1, saveTo: req.SaveTo, owner: req.Owner, done: make(chan Result, 1)}
	if turn && !it.grouped {
		it.turn, it.open, it.grew = req.Turn, true, make(chan struct{}, 1)
	}
	if req.Interrupt {
		s.dropLocked(ErrInterrupted)
	}
	it.track = &Track{ID: it.id, State: "queued", Voices: voices, Queued: time.Now()}
	s.tracks = append(s.tracks, it.track)
	if len(s.tracks) > trackKeep {
		s.tracks = s.tracks[len(s.tracks)-trackKeep:]
	}
	s.queue = append(s.queue, it)
	pos := len(s.queue) - 1
	if s.current != nil {
		pos++
	}
	s.signal()
	return Ticket{ID: it.id, Position: pos, Done: it.done, Note: note, Voices: voices}, nil
}

// Track tells what became of the utterance id; false when it is unknown (too
// old, or said before voice restarted).
func (s *Speaker) Track(id string) (Track, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.tracks) - 1; i >= 0; i-- {
		if s.tracks[i].ID == id {
			t := *s.tracks[i]
			t.Voices = slices.Clone(t.Voices)
			return t, true
		}
	}
	return Track{}, false
}

// endLocked records how an utterance ended; the caller holds s.mu.
func endLocked(it *item, res Result) {
	t := it.track
	if t == nil || t.Ended != nil {
		return
	}
	now := time.Now()
	t.Ended = &now
	t.Engine = res.Engine
	t.Fallback = res.Engine != "" && res.Engine != "cache" && it.primary != "" && res.Engine != it.primary
	if u := it.tags; u != nil {
		t.Sounds, t.Dropped, t.Mood = keys(u.sounds), keys(u.dropped), u.mood
	}
	switch {
	case res.Err == nil:
		t.State = "done"
		if it.saveTo != "" {
			t.Saved = it.saveTo
		}
	case errors.Is(res.Err, ErrStopped):
		t.State = "stopped"
	case errors.Is(res.Err, ErrInterrupted), errors.Is(res.Err, context.Canceled):
		t.State = "interrupted"
	case errors.Is(res.Err, ErrMuted):
		t.State = "muted"
	default:
		t.State, t.Error = "error", res.Err.Error()
	}
}

// Stop silences the current utterance and clears the queue.
func (s *Speaker) Stop() { s.StopAll() }

// StopAll is Stop that reports the sessions whose speech it cut off,
// playing or queued (Request.Owner, no zeros), so they can be told.
func (s *Speaker) StopAll() []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var owners []uint32
	add := func(it *item) {
		if it != nil && it.owner != 0 && it.saveTo == "" && !slices.Contains(owners, it.owner) {
			owners = append(owners, it.owner)
		}
	}
	add(s.current)
	for _, it := range s.queue {
		add(it)
	}
	s.dropLocked(ErrStopped)
	return owners
}

func (s *Speaker) dropLocked(reason error) {
	for _, it := range s.queue {
		s.endedLocked(it, Result{Err: reason})
		it.done <- Result{Err: reason}
	}
	s.queue = nil
	s.cutLocked(reason)
}

// cutLocked cuts off the utterance playing, recording why; the caller holds s.mu.
func (s *Speaker) cutLocked(reason error) {
	if s.current != nil && s.current.why == nil {
		s.current.why = reason
	}
	if s.current != nil {
		s.current.open = false // nothing joins speech that was cut off
	}
	if s.cancel != nil {
		s.cancel()
	}
}

// openTurnLocked is the owner's utterance whose turn still takes text,
// queued or playing; nil when there is none. The caller holds s.mu.
func (s *Speaker) openTurnLocked(owner uint32) *item {
	if it := s.current; it != nil && it.open && it.owner == owner {
		return it
	}
	for _, it := range s.queue {
		if it.open && it.owner == owner {
			return it
		}
	}
	return nil
}

// endTurnLocked closes the owner's open turn: what it holds is said, and
// nothing more joins it. The caller holds s.mu.
func (s *Speaker) endTurnLocked(owner uint32) {
	if owner == 0 {
		return
	}
	for it := s.openTurnLocked(owner); it != nil; it = s.openTurnLocked(owner) {
		it.open = false
		select {
		case it.grew <- struct{}{}:
		default:
		}
	}
}

// EndTurn closes the owner's open turn (Request.Turn): its reply is
// complete, so voice stops waiting for more of it.
func (s *Speaker) EndTurn(owner uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endTurnLocked(owner)
}

// turnIdle is how long a turn waits for its next piece once everything in
// it has been heard, before it ends by itself.
var turnIdle = 2 * time.Second // a variable for tests

// moreParts waits in play for the turn's parts after the first have of
// them: what an append brought, or false once the turn has closed, ctx
// ended, or turnIdle passed after all that was sent was heard (heard()).
func (s *Speaker) moreParts(ctx context.Context, it *item, have int, heard func() bool) ([]part, bool) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var quietSince time.Time
	for {
		s.mu.Lock()
		if len(it.parts) > have {
			more := slices.Clone(it.parts[have:])
			s.mu.Unlock()
			return more, true
		}
		if !it.open {
			s.mu.Unlock()
			return nil, false
		}
		if !quietSince.IsZero() && time.Since(quietSince) >= turnIdle {
			it.open = false // ends by itself; a later piece starts a new utterance
			s.mu.Unlock()
			return nil, false
		}
		s.mu.Unlock()
		select {
		case <-it.grew:
		case <-tick.C:
			if quietSince.IsZero() && heard() {
				quietSince = time.Now()
			}
		case <-ctx.Done():
			return nil, false
		}
	}
}

// How long a turn in a GPU voice waits for its next pieces before it
// renders what it has: until none came for gatherQuiet, gatherMax at most,
// or a batch is full.
var (
	gatherQuiet = 700 * time.Millisecond
	gatherMax   = 2500 * time.Millisecond
)

// gather waits for a turn's pieces after the first have of them to come
// together (see gatherQuiet) and returns them; none when the turn closed.
func (s *Speaker) gather(ctx context.Context, it *item, have int) []part {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	start, last, n := time.Now(), time.Now(), have
	for {
		s.mu.Lock()
		if len(it.parts) != n {
			n, last = len(it.parts), time.Now()
		}
		open := it.open
		s.mu.Unlock()
		if !open || n-have+1 >= batchSize || time.Since(last) >= gatherQuiet || time.Since(start) >= gatherMax {
			break
		}
		select {
		case <-tick.C:
		case <-it.grew:
		case <-ctx.Done():
			return nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(it.parts[have:])
}

// StopOwner silences one session's speech: its queued utterances and the
// one playing, when it is theirs. Other sessions' speech goes on. It
// reports how many utterances it stopped.
func (s *Speaker) StopOwner(owner uint32) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	kept := s.queue[:0]
	for _, it := range s.queue {
		if it.owner != owner {
			kept = append(kept, it)
			continue
		}
		s.endedLocked(it, Result{Err: ErrStopped})
		it.done <- Result{Err: ErrStopped}
		n++
	}
	clear(s.queue[len(kept):])
	s.queue = kept
	if s.current != nil && s.current.owner == owner {
		s.cutLocked(ErrStopped)
		n++
	}
	return n
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
		l.ID, l.Text = s.current.id, blankTags(s.current.text)
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
	if t := it.track; t != nil && t.Heard == nil {
		at := it.line.At
		t.State, t.Heard = "playing", &at
	}
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

// ReplaceEngine gives an engine a new config (a speech server's new
// address): the one in use closes and the next use makes it again.
func (s *Speaker) ReplaceEngine(name string, cfg engine.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Engines[name] = cfg
	if e := s.engines[name]; e != nil {
		e.Close()
		delete(s.engines, name)
		delete(s.used, name)
	}
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
			if it.track != nil {
				it.track.State = "rendering"
			}
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
		cut := it.why != nil && ctx.Err() != nil // checked before the cancel below
		it.open = false                          // said: what comes next starts anew
		s.cancel()
		s.current, s.cancel = nil, nil
		if cut {
			res.Err = it.why // stopped or interrupted, as the caller said
		} else if res.Err != nil && !errors.Is(res.Err, context.Canceled) {
			s.lastErr = res.Err.Error()
		}
		s.endedLocked(it, res)
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
	pcm  []float32
	err  error
	gap  bool   // a pause or an effect's tail between parts: no sentence of its own
	text string // the sentence it says, to place it in the text for Status
}

// What an engine's padding around a sentence is cut to, in seconds
// (audio.TrimSilence): a short breath, the same with every engine.
const (
	sentenceLead = 0.05
	sentenceTail = 0.25
)

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
	voice := func(p part) (voiced, error) {
		v := voiced{part: p, choice: p.preset.For(it.lang)}
		var err error
		if v.choice.Voice, err = resolveVoice(v.choice.Voice); err != nil {
			return v, err
		}
		// Effects keep state across sentences; Resolve already validated them.
		if v.effects, err = audio.ParseChain(p.preset.DSP, audio.DeviceRate); err != nil {
			return v, err
		}
		// Pitch is shifted by playing the audio at a different rate. The engine
		// speaks faster or slower by the same factor, so tempo stays as set.
		v.pitch = math.Pow(2, v.choice.Pitch/12)
		v.sentences = Split(p.text, minSentence)
		return v, nil
	}
	// The parts so far: an open turn's grow while it plays (moreParts).
	s.mu.Lock()
	parts := slices.Clone(it.parts)
	s.mu.Unlock()
	var vs []voiced
	for _, p := range parts {
		v, err := voice(p)
		if err != nil {
			return Result{Err: err}
		}
		vs = append(vs, v)
	}
	first := vs[0]
	tu := newTagUse()
	it.tags = tu
	if len(vs) == 1 && len(first.choice.Engines) > 0 {
		it.primary = first.choice.Engines[0] // one voice: another engine is a fallback
	}

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
	// sent counts the pieces handed to the player (the renderer's own),
	// played those it finished: equal, everything rendered has been heard.
	sent, played := 0, atomic.Int64{}
	// used names the engines that spoke, in order (one, or a scene's).
	var used string
	noteUsed := func(name string) {
		if !slices.Contains(strings.Split(used, ", "), name) {
			if used != "" {
				used += ", "
			}
			used += name
		}
	}
	go func() {
		defer close(ch)
		put := func(r rendered) bool {
			select {
			case ch <- r:
				sent++
				return true
			case <-ctx.Done():
				return false
			}
		}
		// renderPart renders one part sentence by sentence (or in batches),
		// handing each piece to out; false when it stopped (an error went to
		// out, or the context ended). Its effects' tail is tail's.
		renderPart := func(v *voiced, out func(rendered) bool) bool {
			engines := v.choice.Engines
			send := func(pcm []float32, text string) bool {
				// The quiet an engine pads a sentence with goes: the pause between
				// sentences is voice's, the same for every engine.
				pcm = audio.TrimSilence(pcm, audio.DeviceRate, sentenceLead, sentenceTail)
				v.effects.Process(pcm)
				audio.Gain(pcm, v.preset.Volume)
				return out(rendered{pcm: pcm, text: text})
			}
			for k := 0; k < len(v.sentences); {
				// An engine slower than its own speech (a GPU model) renders a few
				// sentences at once, in about the time of one, from the first
				// sentence on: speech starts a moment later and then flows, rather
				// than a short sentence and a long silence. The next batch renders
				// while this one plays. A batch that fails goes sentence by sentence.
				if m := min(batchSize, len(v.sentences)-k); m > 1 && engines[0] == v.choice.Engines[0] {
					if pcms, err := s.renderBatch(ctx, engines[0], v.pitch, it.lang, v.choice, v.sentences[k:k+m], v.mood, tu); err == nil {
						noteUsed(engines[0])
						for j, pcm := range pcms {
							if !send(pcm, v.sentences[k+j]) {
								return false
							}
						}
						k += m
						continue
					}
				}
				req := engine.Request{Text: v.sentences[k], Lang: it.lang, Voice: v.choice.Voice, Speed: v.choice.Speed / v.pitch}
				pcm, name, rest, err := s.render(ctx, engines, v.choice.Engines[0], v.pitch, req, v.mood, tu)
				if err != nil {
					out(rendered{err: err})
					return false
				}
				// Stay on the engine that worked so the voice does not change
				// mid-line; later fallbacks remain for failures.
				engines = rest
				noteUsed(name)
				if !send(pcm, v.sentences[k]) {
					return false
				}
				k++
			}
			return true
		}
		// tail: a part's effects ring out before the next voice.
		tail := func(v *voiced, out func(rendered) bool) bool {
			if t := v.effects.Tail(); len(t) > 0 {
				audio.Gain(t, v.preset.Volume)
				return out(rendered{pcm: t, gap: true})
			}
			return true
		}
		// Between parts: the break the script asked for, else a short breath
		// when the voice changes (a turn in the dialogue).
		gapBefore := func(n int) float64 {
			if n == 0 {
				return 0
			}
			gap := vs[n-1].pause
			if gap == 0 && vs[n-1].preset.Name != vs[n].preset.Name {
				gap = lineGap
			}
			return gap + vs[n].before
		}

		if !it.grouped {
			if it.lead > 0 && !put(rendered{pcm: audio.Silence(audio.DeviceRate, it.lead), gap: true}) {
				return
			}
			// took counts the item's parts taken into vs, which merging shortens.
			took := len(vs)
			// addParts voices a turn's new pieces; false after an error went out.
			addParts := func(more []part) bool {
				took += len(more)
				for _, p := range more {
					v, err := voice(p)
					if err != nil {
						put(rendered{err: err})
						return false
					}
					vs = append(vs, v)
				}
				return true
			}
			// merge joins a turn's pieces from vs[from] on that go on in the same
			// voice and mood, with no break between, into one part: its sentences
			// then render in batches, as one long text's would. A GPU voice renders
			// a batch in about the time of one sentence, slower than speech.
			merge := func(from int) {
				out := vs[:from]
				for _, v := range vs[from:] {
					if k := len(out) - 1; k >= from && out[k].preset.Name == v.preset.Name && out[k].mood == v.mood && out[k].pause == 0 && v.before == 0 {
						out[k].text += " " + v.text
						out[k].sentences = append(out[k].sentences, v.sentences...)
						out[k].pause = v.pause
						continue
					}
					out = append(out, v)
				}
				vs = out
			}
			n := 0
			for {
				if it.turn != "" && n < len(vs) {
					// A GPU voice waits a moment for the reply's next pieces, so they
					// render together instead of one slow sentence at a time.
					if s.heavyEngine(vs[n].choice.Engines[0]) && !addParts(s.gather(ctx, it, took)) {
						return
					}
					merge(n)
				}
				for ; n < len(vs); n++ {
					if n > 0 {
						// A turn's next piece in the same voice carries on its effects,
						// with no tail between: one utterance. Else the tail rings out.
						if it.turn != "" && vs[n-1].preset.Name == vs[n].preset.Name {
							vs[n].effects = vs[n-1].effects
						} else if !tail(&vs[n-1], put) {
							return
						}
					}
					if g := gapBefore(n); g > 0 && !put(rendered{pcm: audio.Silence(audio.DeviceRate, g), gap: true}) {
						return
					}
					if !renderPart(&vs[n], put) {
						return
					}
				}
				if it.turn == "" {
					break
				}
				// An open turn: the reply's next piece, rendered as it comes.
				more, ok := s.moreParts(ctx, it, took, func() bool { return played.Load() == int64(sent) })
				if !ok || !addParts(more) {
					break
				}
			}
			if len(vs) > 0 {
				tail(&vs[len(vs)-1], put)
			}
			return
		}

		// Rendered first, then heard: one engine at a time, all of its parts
		// in one go, so a scene across GPU models does not load and unload
		// them line by line. A GPU model the next one may not fit beside is
		// unloaded before it. Then the scene plays in its own order.
		results := make([][]rendered, len(vs))
		firsts := make([]string, len(vs))
		for n, v := range vs {
			firsts[n] = v.choice.Engines[0]
		}
		groups := engineGroups(firsts)
		for gi, g := range groups {
			s.progress(it, fmt.Sprintf("rendering voice %d of %d (%s)", gi+1, len(groups), g.engine))
			if gi > 0 {
				s.makeRoom(ctx, groups[gi-1].engine, g.engine)
			}
			for _, n := range g.parts {
				collect := func(r rendered) bool {
					if r.err != nil {
						put(r)
						return false
					}
					results[n] = append(results[n], r)
					return ctx.Err() == nil
				}
				if !renderPart(&vs[n], collect) || !tail(&vs[n], collect) {
					return
				}
			}
		}
		s.progress(it, "")
		if it.lead > 0 && !put(rendered{pcm: audio.Silence(audio.DeviceRate, it.lead), gap: true}) {
			return
		}
		for n := range vs {
			if g := gapBefore(n); g > 0 && !put(rendered{pcm: audio.Silence(audio.DeviceRate, g), gap: true}) {
				return
			}
			for _, r := range results[n] {
				if !put(r) {
					return
				}
			}
		}
	}()

	// Where each sentence sits in the text, for Status; an open turn's text
	// grows as it plays.
	at := 0
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
			s.mu.Lock()
			runes := []rune(it.text)
			s.mu.Unlock()
			at = min(at, len(runes))
			from, to := at, len(runes)
			if r.text != "" {
				if k := strings.Index(string(runes[at:]), r.text); k >= 0 {
					from = at + len([]rune(string(runes[at:])[:k]))
					to = from + len([]rune(r.text))
				}
			}
			at = to
			s.playing(it, from, to, r.pcm)
		}
		err := s.out.Play(ctx, r.pcm)
		played.Add(1)
		if err != nil {
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
func (s *Speaker) renderBatch(ctx context.Context, name string, pitch float64, lang string, choice preset.Choice, sentences []string, mood string, tu *tagUse) ([][]float32, error) {
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
	x := s.expressive(name)
	reqs := make([]engine.Request, len(sentences))
	for i, t := range sentences {
		text, extra := forEngine(x, t, mood, tu)
		reqs[i] = engine.Request{Text: text, Lang: lang, Voice: choice.Voice, Speed: choice.Speed / pitch, Extra: extra}
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

// PresetExpressive is what preset name's own engine does with voice's tags
// and moods (its first engine for the default language).
func (s *Speaker) PresetExpressive(name string) Expressive {
	r, err := s.cfg.Resolve(name)
	if err != nil {
		return Expressive{Emotion: "none"}
	}
	if c := r.For(s.cfg.DefaultLang); len(c.Engines) > 0 {
		return s.expressive(c.Engines[0])
	}
	return Expressive{Emotion: "none"}
}

// heavyEngine reports whether an engine runs a model on a server (voice's
// GPU sidecar, or a speech server of its own): one that may not fit beside
// another.
func (s *Speaker) heavyEngine(name string) bool {
	t, _ := s.cfg.Engines[name]["type"].(string)
	return t == "http" || t == "openai"
}

// renderFirst decides whether an utterance is rendered before it is heard.
func (s *Speaker) renderFirst(mode, saveTo string, parts []part) bool {
	switch {
	case saveTo != "" || mode == "render":
		return true
	case mode == "play":
		return false
	}
	heavy := map[string]bool{}
	for _, p := range parts {
		if c := p.preset.For(s.cfg.DefaultLang); len(c.Engines) > 0 && s.heavyEngine(c.Engines[0]) {
			heavy[c.Engines[0]] = true
		}
	}
	return len(heavy) > 1
}

// engineGroup is the parts one engine renders, in their order.
type engineGroup struct {
	engine string
	parts  []int
}

// engineGroups gathers parts by the engine that renders each, engines in
// the order they first speak.
func engineGroups(engines []string) []engineGroup {
	var out []engineGroup
	at := map[string]int{}
	for n, e := range engines {
		i, ok := at[e]
		if !ok {
			i = len(out)
			at[e] = i
			out = append(out, engineGroup{engine: e})
		}
		out[i].parts = append(out[i].parts, n)
	}
	return out
}

// makeRoom unloads engine prev before next renders, when both run GPU
// models and prev can be unloaded (voice's sidecar): two models may not fit
// on the graphics card together.
func (s *Speaker) makeRoom(ctx context.Context, prev, next string) {
	if prev == next || !s.heavyEngine(prev) || !s.heavyEngine(next) {
		return
	}
	e, err := s.Engine(prev)
	if err != nil {
		return
	}
	if u, ok := e.(interface{ Unload(context.Context) }); ok {
		if Logf != nil {
			Logf("speech: unloading %s to make room for %s", prev, next)
		}
		u.Unload(ctx)
	}
}

// progress records how far rendering an utterance first has got.
func (s *Speaker) progress(it *item, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it.track != nil {
		it.track.Progress = msg
	}
}

// expressive is what engine name declares about tags and moods.
func (s *Speaker) expressive(name string) Expressive {
	if c, ok := s.cfg.Engines[name]; ok {
		return ParseExpressive(c["expressive"])
	}
	return Expressive{Emotion: "none"}
}

// render tries engines in order and returns audio at the device rate, the
// engine used and the engines still eligible for the following sentences.
func (s *Speaker) render(ctx context.Context, engines []string, primary string, pitch float64, req engine.Request, mood string, tu *tagUse) ([]float32, string, []string, error) {
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
			// voice's tags and the mood, as this engine has them; what it
			// cannot make is left out, never read aloud.
			r.Text, r.Extra = forEngine(s.expressive(name), req.Text, mood, tu)
			if strings.TrimSpace(r.Text) == "" {
				return nil, name, engines[i:], nil // only sounds it cannot make
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
