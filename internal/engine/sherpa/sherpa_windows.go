package sherpa

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"voice/internal/audio"
	"voice/internal/engine"
)

func init() { engine.Register("sherpa", New) }

type api struct {
	create, destroy, sampleRate, numSpeakers, generate, destroyAudio uintptr
}

var (
	loadOnce sync.Once
	lib      api
	libDLL   windows.Handle
	loadErr  error
)

// load opens sherpa-onnx-c-api.dll from the runtime directory. Loading by
// full path with LOAD_WITH_ALTERED_SEARCH_PATH resolves onnxruntime.dll from
// the same directory.
func load() (api, error) {
	loadOnce.Do(func() {
		path := filepath.Join(engine.Dirs.Runtime, "sherpa-onnx-c-api.dll")
		if _, err := os.Stat(path); err != nil {
			loadErr = fmt.Errorf("sherpa-onnx runtime not installed (%s); run `voice models pull runtime`", path)
			return
		}
		h, err := windows.LoadLibraryEx(path, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
		if err != nil {
			loadErr = fmt.Errorf("load %s: %w", path, err)
			return
		}
		libDLL = h
		procs := map[string]*uintptr{
			"SherpaOnnxCreateOfflineTts":                &lib.create,
			"SherpaOnnxDestroyOfflineTts":               &lib.destroy,
			"SherpaOnnxOfflineTtsSampleRate":            &lib.sampleRate,
			"SherpaOnnxOfflineTtsNumSpeakers":           &lib.numSpeakers,
			"SherpaOnnxOfflineTtsGenerateWithConfig":    &lib.generate,
			"SherpaOnnxDestroyOfflineTtsGeneratedAudio": &lib.destroyAudio,
		}
		for name, dst := range procs {
			p, err := windows.GetProcAddress(h, name)
			if err != nil {
				loadErr = fmt.Errorf("sherpa-onnx runtime lacks %s (need %s): %w", name, Version, err)
				return
			}
			*dst = p
		}
	})
	return lib, loadErr
}

// procs resolves functions of the loaded runtime by name.
func procs(names ...string) ([]uintptr, error) {
	if _, err := load(); err != nil {
		return nil, err
	}
	out := make([]uintptr, len(names))
	for i, name := range names {
		p, err := windows.GetProcAddress(libDLL, name)
		if err != nil {
			return nil, fmt.Errorf("sherpa-onnx runtime lacks %s (need %s): %w", name, Version, err)
		}
		out[i] = p
	}
	return out, nil
}

func call(fn uintptr, args ...uintptr) uintptr {
	// Arguments are integers or pointers; most floats travel inside structs.
	// A float argument among the first four is passed as its bit pattern:
	// Go's Windows syscall path copies those registers into XMM0-XMM3 too.
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

// cString copies a NUL-terminated C string.
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); ; i++ {
		c := *(*byte)(ptr(p + i))
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
	}
}

// ptr converts a C pointer returned as uintptr; the memory is owned by C.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

type Engine struct {
	name     string
	family   string // kokoro | vits | pocket | zipvoice
	lang     string
	steps    int32
	refs     map[string]refAudio // decoded reference clips by path
	dir      string
	mu       sync.Mutex
	tts      uintptr
	rate     int
	speakers []string       // index = sid
	mixes    map[string]int // mix key -> sid
	keep     [][]byte       // C strings referenced by the config
	defVoice string
}

// New loads a model. Config keys:
//
//	model    model directory (relative to the models dir)
//	family   kokoro | vits | pocket | zipvoice (default: detected from files)
//	lang     Kokoro language, e.g. en-us, en-gb (default en-us)
//	lexicon  lexicon file(s) inside the model dir, comma-separated
//	threads  inference threads (default 4)
//	voice    default voice name; for cloning models a reference clip path
//	steps    flow-matching steps for ZipVoice (default 4)
func New(name string, cfg engine.Config) (engine.Engine, error) {
	l, err := load()
	if err != nil {
		return nil, err
	}
	dir := cfg.String("model")
	if dir == "" {
		return nil, fmt.Errorf("engine %q: sherpa needs model", name)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(engine.Dirs.Models, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("engine %q: model %s not installed; see `voice models`", name, dir)
	}
	e := &Engine{name: name, family: cfg.String("family"), lang: cfg.String("lang"), defVoice: cfg.String("voice"), mixes: map[string]int{}, refs: map[string]refAudio{}, dir: dir, steps: 4}
	if n, ok := cfg["steps"].(int64); ok && n > 0 {
		e.steps = int32(n)
	}
	if e.family == "" {
		e.family = detectFamily(dir)
	}
	var need []string // files sherpa-onnx will open
	file := func(pattern string) uintptr {
		p := pick(dir, pattern)
		need = append(need, p)
		return e.cstr(p)
	}
	threads := int32(4)
	if n, ok := cfg["threads"].(int64); ok && n > 0 {
		threads = int32(n)
	}

	var c cTTSConfig
	c.Model.NumThreads = threads
	c.Model.Provider = e.cstr("cpu")
	c.MaxNumSentences = 1
	c.SilenceScale = 0.2
	data := e.optional(dir, "espeak-ng-data")

	switch e.family {
	case "kokoro":
		if e.lang == "" {
			e.lang = "en-us"
		}
		e.speakers = kokoroSpeakers(dir)
		voices := filepath.Join(dir, "voices.bin")
		if mixes, _ := cfg[engine.MixesKey].([]engine.Voice); len(mixes) > 0 {
			if voices, err = e.bakeMixes(dir, voices, mixes); err != nil {
				return nil, fmt.Errorf("engine %q: %w", name, err)
			}
		}
		need = append(need, voices)
		c.Model.Kokoro = cKokoro{
			Model:   file("model.onnx"),
			Voices:  e.cstr(voices),
			Tokens:  file("tokens.txt"),
			DataDir: data,
			DictDir: e.optional(dir, "dict"),
			Lexicon: e.lexicons(dir, cfg.String("lexicon")),
			Lang:    e.cstr(espeakVoice(e.lang)),
			// Speed is set per request; length_scale stays neutral.
			LengthScale: 1,
		}
	case "vits":
		c.Model.Vits = cVits{
			Model:       file("*.onnx"),
			Tokens:      file("tokens.txt"),
			DataDir:     data,
			Lexicon:     e.lexicons(dir, cfg.String("lexicon")),
			NoiseScale:  0.667,
			NoiseScaleW: 0.8,
			LengthScale: 1,
		}
	case "pocket":
		c.Model.Pocket = cPocket{
			LmFlow:                      file("lm_flow*.onnx"),
			LmMain:                      file("lm_main*.onnx"),
			Encoder:                     file("encoder*.onnx"),
			Decoder:                     file("decoder*.onnx"),
			TextConditioner:             file("text_conditioner*.onnx"),
			VocabJSON:                   file("vocab.json"),
			TokenScoresJSON:             file("token_scores.json"),
			VoiceEmbeddingCacheCapacity: 8,
		}
	case "zipvoice":
		c.Model.Zipvoice = cZipvoice{
			Tokens:        file("tokens.txt"),
			Encoder:       file("encoder*.onnx"),
			Decoder:       file("decoder*.onnx"),
			Vocoder:       file("vocos*.onnx"),
			DataDir:       data,
			Lexicon:       e.optional(dir, "lexicon.txt"),
			FeatScale:     0.1,
			TShift:        0.5,
			TargetRMS:     0.1,
			GuidanceScale: 1.0,
		}
	case "supertonic":
		// Supertonic 3: 31 languages, chosen per request; ten speakers by sid.
		c.Model.Supertonic = cSupertonic{
			DurationPredictor: file("duration_predictor*.onnx"),
			TextEncoder:       file("text_encoder*.onnx"),
			VectorEstimator:   file("vector_estimator*.onnx"),
			Vocoder:           file("vocoder*.onnx"),
			TTSJSON:           file("tts.json"),
			UnicodeIndexer:    file("unicode_indexer.bin"),
			VoiceStyle:        file("voice.bin"),
		}
		if _, ok := cfg["steps"].(int64); !ok {
			e.steps = 8 // the model's own default
		}
	default:
		return nil, fmt.Errorf("engine %q: unsupported family %q", name, e.family)
	}

	// sherpa-onnx exits the whole process on some configuration errors, so
	// check what it will open first.
	for _, f := range need {
		if !exists(f) {
			return nil, fmt.Errorf("engine %q: missing model file %s; reinstall with `voice models pull`", name, f)
		}
	}
	e.tts = call(l.create, uintptr(unsafe.Pointer(&c)))
	runtime.KeepAlive(&c)
	if e.tts == 0 {
		return nil, fmt.Errorf("engine %q: sherpa-onnx rejected the model in %s", name, dir)
	}
	e.rate = int(int32(call(l.sampleRate, e.tts)))
	n := int(int32(call(l.numSpeakers, e.tts)))
	switch {
	case e.family == "supertonic" && n > 1:
		// Its speakers have numbers only; voices name them 0..n-1.
		e.speakers = make([]string, n)
		for i := range e.speakers {
			e.speakers[i] = fmt.Sprint(i)
		}
	case e.family != "kokoro":
		e.speakers = []string{"default"}
	}
	if e.family == "kokoro" && n != len(e.speakers) {
		// Unknown voice list for this model; address voices by number.
		e.speakers = make([]string, n-len(e.mixes))
		for i := range e.speakers {
			e.speakers[i] = fmt.Sprint(i)
		}
	}
	return e, nil
}

func (e *Engine) cstr(s string) uintptr {
	b := append([]byte(s), 0)
	e.keep = append(e.keep, b)
	return uintptr(unsafe.Pointer(&b[0]))
}

// goString returns a string previously passed through cstr.
func (e *Engine) goString(p uintptr) string {
	for _, b := range e.keep {
		if uintptr(unsafe.Pointer(&b[0])) == p {
			return string(b[:len(b)-1])
		}
	}
	return ""
}

func (e *Engine) optional(dir, sub string) uintptr {
	if p := filepath.Join(dir, sub); exists(p) {
		return e.cstr(p)
	}
	return 0
}

// lexicons picks lexicon files: the configured list, or for Kokoro the
// English lexicon matching the language. Words missing from it fall back to
// espeak-ng inside sherpa-onnx.
func (e *Engine) lexicons(dir, configured string) uintptr {
	var files []string
	if configured != "" {
		for _, f := range strings.Split(configured, ",") {
			files = append(files, filepath.Join(dir, strings.TrimSpace(f)))
		}
	} else if e.family == "kokoro" {
		name := "lexicon-us-en.txt"
		if e.lang == "en-gb" {
			name = "lexicon-gb-en.txt"
		}
		if p := filepath.Join(dir, name); exists(p) {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return 0
	}
	return e.cstr(strings.Join(files, ","))
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tts != 0 {
		call(lib.destroy, e.tts)
		e.tts = 0
	}
	return nil
}

func (e *Engine) Voices(context.Context) ([]engine.VoiceInfo, error) {
	out := make([]engine.VoiceInfo, 0, len(e.speakers))
	for _, s := range e.speakers {
		out = append(out, engine.VoiceInfo{Name: s, Lang: kokoroLang(s, e.lang), Gender: kokoroGender(s)})
	}
	return out, nil
}

func (e *Engine) sid(v engine.Voice) (int, error) {
	if len(v.Mix) > 1 {
		if id, ok := e.mixes[mixKey(v.Mix)]; ok {
			return id, nil
		}
		return 0, fmt.Errorf("engine %q: voice mix %s was not prepared at startup", e.name, mixKey(v.Mix))
	}
	name := v.Primary()
	if name == "" {
		name = e.defVoice
	}
	if name == "" || e.family == "vits" {
		return 0, nil
	}
	for i, s := range e.speakers {
		if s == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("engine %q: unknown voice %q (see `voice voices -engine %s`)", e.name, name, e.name)
}

func (e *Engine) clones() bool { return e.family == "pocket" || e.family == "zipvoice" }

type refAudio struct {
	pcm  []float32
	rate int
}

// reference loads a clip once per engine; cloned voices repeat a lot.
func (e *Engine) reference(path string) (refAudio, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.dir, path)
	}
	if r, ok := e.refs[path]; ok {
		return r, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return refAudio{}, fmt.Errorf("engine %q: reference clip: %w", e.name, err)
	}
	pcm, rate, err := audio.DecodeWAV(b)
	if err != nil || len(pcm) == 0 {
		return refAudio{}, fmt.Errorf("engine %q: reference clip %s: %v", e.name, path, err)
	}
	r := refAudio{pcm: pcm, rate: rate}
	e.refs[path] = r
	return r, nil
}

func (e *Engine) Synthesize(ctx context.Context, req engine.Request, sink func(engine.Chunk) error) error {
	if req.Voice.Ref != "" && !e.clones() {
		return fmt.Errorf("engine %q: %w", e.name, engine.ErrNoCloning)
	}
	speed := req.Speed
	if speed <= 0 {
		speed = 1
	}
	text := append([]byte(req.Text), 0)
	gen := cGenerationConfig{SilenceScale: 0.2, Speed: float32(speed)}
	var keepRef []float32
	var keepText []byte
	if e.clones() {
		path, refText := req.Voice.Ref, req.Voice.RefText
		if path == "" {
			path = e.defVoice
		}
		if path == "" {
			return fmt.Errorf("engine %q: needs a cloned voice (voice = \"clone:<name>\"); see `voice clone`", e.name)
		}
		e.mu.Lock()
		ref, err := e.reference(path)
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if e.family == "zipvoice" && strings.TrimSpace(refText) == "" {
			return fmt.Errorf("engine %q: needs the transcript of the reference clip; add it with `voice clone add -text`", e.name)
		}
		keepRef = ref.pcm
		gen.ReferenceAudio = uintptr(unsafe.Pointer(&keepRef[0]))
		gen.ReferenceAudioLen = int32(len(keepRef))
		gen.ReferenceSampleRate = int32(ref.rate)
		if refText != "" {
			keepText = append([]byte(refText), 0)
			gen.ReferenceText = uintptr(unsafe.Pointer(&keepText[0]))
		}
		if e.family == "zipvoice" {
			gen.NumSteps = e.steps
		}
	} else {
		sid, err := e.sid(req.Voice)
		if err != nil {
			return err
		}
		gen.Sid = int32(sid)
	}
	var keepExtra []byte
	if e.family == "supertonic" {
		// The language goes with each request: the call's, else the engine's.
		lang := strings.ToLower(strings.SplitN(strings.TrimSpace(req.Lang), "-", 2)[0])
		if lang == "" || lang == "auto" {
			lang = strings.ToLower(strings.SplitN(e.lang, "-", 2)[0])
		}
		if lang == "" {
			lang = "en"
		}
		keepExtra = append([]byte(`{"lang":"`+lang+`"}`), 0)
		gen.Extra = uintptr(unsafe.Pointer(&keepExtra[0]))
		gen.NumSteps = e.steps
	}

	e.mu.Lock()
	if e.tts == 0 {
		e.mu.Unlock()
		return errors.New("sherpa: engine closed")
	}
	audio := call(lib.generate, e.tts, uintptr(unsafe.Pointer(&text[0])), uintptr(unsafe.Pointer(&gen)), 0, 0)
	runtime.KeepAlive(text)
	runtime.KeepAlive(&gen)
	runtime.KeepAlive(keepRef)
	runtime.KeepAlive(keepText)
	runtime.KeepAlive(keepExtra)
	e.mu.Unlock()
	if audio == 0 {
		return fmt.Errorf("engine %q: generation failed", e.name)
	}
	a := (*cGeneratedAudio)(ptr(audio))
	pcm := make([]float32, a.N)
	if a.N > 0 {
		copy(pcm, unsafe.Slice((*float32)(ptr(a.Samples)), a.N))
	}
	rate := int(a.SampleRate)
	call(lib.destroyAudio, audio)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sink(engine.Chunk{PCM: pcm, SampleRate: rate})
}

// bakeMixes writes a voices file in which mixes replace the last voices of
// the model. Kokoro voices are style tensors of equal size, so a mix is their
// weighted average. sherpa-onnx requires the file to hold exactly the
// model's voice count, so mixes cannot be appended; the replaced voices
// (last in the list, non-English) become unavailable in this engine. The
// file is cached by content key.
func (e *Engine) bakeMixes(dir, voicesPath string, mixes []engine.Voice) (string, error) {
	raw, err := os.ReadFile(voicesPath)
	if err != nil {
		return "", err
	}
	n := len(e.speakers)
	if n == 0 || len(raw)%(n*4) != 0 {
		return "", fmt.Errorf("cannot mix voices: unknown voice list for %s", dir)
	}
	per := len(raw) / n / 4 // floats per voice
	base := unsafe.Slice((*float32)(unsafe.Pointer(&raw[0])), len(raw)/4)

	var keys []string
	byKey := map[string]map[string]float64{}
	for _, m := range mixes {
		if len(m.Mix) > 1 {
			k := mixKey(m.Mix)
			if _, dup := byKey[k]; !dup {
				keys = append(keys, k)
				byKey[k] = m.Mix
			}
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return voicesPath, nil
	}
	used := map[string]bool{}
	for _, k := range keys {
		for name := range byKey[k] {
			used[name] = true
		}
	}
	// Pick replacement slots from the end, never a voice a mix is made of.
	var slots []int
	for i := n - 1; i >= 0 && len(slots) < len(keys); i-- {
		if !used[e.speakers[i]] && kokoroLang(e.speakers[i], "") != "en" {
			slots = append(slots, i)
		}
	}
	if len(slots) < len(keys) {
		return "", fmt.Errorf("too many voice mixes (%d) for %s", len(keys), dir)
	}
	out := append([]float32(nil), base...)
	for i, k := range keys {
		var total float64
		acc := make([]float64, per)
		for name, w := range byKey[k] {
			idx := -1
			for j, s := range e.speakers {
				if s == name {
					idx = j
				}
			}
			if idx < 0 {
				return "", fmt.Errorf("voice mix %s: unknown voice %q", k, name)
			}
			total += w
			for j, v := range base[idx*per : (idx+1)*per] {
				acc[j] += w * float64(v)
			}
		}
		if total <= 0 {
			return "", fmt.Errorf("voice mix %s: weights must be positive", k)
		}
		slot := slots[i]
		dst := out[slot*per : (slot+1)*per]
		for j, v := range acc {
			dst[j] = float32(v / total)
		}
		e.mixes[k] = slot
		e.speakers[slot] = "mix:" + k
	}
	path := filepath.Join(dir, fmt.Sprintf("voices.mix-%08x.bin", hash(strings.Join(keys, "|"))))
	if exists(path) {
		return path, nil
	}
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(&out[0])), len(out)*4)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, bytes, 0o644); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

func mixKey(mix map[string]float64) string {
	names := make([]string, 0, len(mix))
	for n := range mix {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s:%g", n, math.Round(mix[n]*1000)/1000)
	}
	return strings.Join(parts, "+")
}

func hash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	return h
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// pick returns the first file matching pattern in dir, or the would-be path
// so the missing-file check can name it.
func pick(dir, pattern string) string {
	if m, _ := filepath.Glob(filepath.Join(dir, pattern)); len(m) > 0 {
		return m[0]
	}
	return filepath.Join(dir, pattern)
}

func detectFamily(dir string) string {
	has := func(pattern string) bool {
		m, _ := filepath.Glob(filepath.Join(dir, pattern))
		return len(m) > 0
	}
	switch {
	case has("vector_estimator*.onnx"):
		return "supertonic"
	case has("voices.bin"):
		return "kokoro"
	case has("lm_main*.onnx"):
		return "pocket"
	case has("vocos*.onnx") && has("encoder*.onnx"):
		return "zipvoice"
	}
	return "vits"
}
