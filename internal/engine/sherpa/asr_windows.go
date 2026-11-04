package sherpa

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// cbuf is a zeroed C struct image whose strings stay alive with it.
type cbuf struct {
	b    []byte
	keep [][]byte
}

func newCBuf(layoutKey string) *cbuf { return &cbuf{b: make([]byte, asrLayout[layoutKey])} }

func (c *cbuf) str(off string, s string) {
	if s == "" {
		return
	}
	v := append([]byte(s), 0)
	c.keep = append(c.keep, v)
	binary.LittleEndian.PutUint64(c.b[asrLayout[off]:], uint64(uintptr(unsafe.Pointer(&v[0]))))
}

func (c *cbuf) i32(off string, v int32) {
	binary.LittleEndian.PutUint32(c.b[asrLayout[off]:], uint32(v))
}

func (c *cbuf) f32(off string, v float32) {
	binary.LittleEndian.PutUint32(c.b[asrLayout[off]:], math.Float32bits(v))
}

func (c *cbuf) ptr() uintptr { return uintptr(unsafe.Pointer(&c.b[0])) }

// Recognizer transcribes complete utterances with an offline model:
// NeMo transducers (Parakeet) or Whisper.
type Recognizer struct {
	mu     sync.Mutex
	h      uintptr
	fn     []uintptr
	Family string

	// Whisper's language changes in place (SherpaOnnxOfflineRecognizerSetConfig,
	// 0 in runtimes without it): config builds the configuration for a
	// language, base is the one it was made with ("" detects), lang the one
	// set now, conf keeps that configuration alive.
	setConfig  uintptr
	config     func(lang string) *cbuf
	base, lang string
	conf       *cbuf
}

const (
	fCreateRec = iota
	fDestroyRec
	fCreateStream
	fDestroyStream
	fAccept
	fDecode
	fResult
	fDestroyResult
)

// NewRecognizer loads the model in dir. lang is the Whisper language ("" to
// detect); transducers detect nothing and ignore it.
func NewRecognizer(dir, lang string, threads int) (*Recognizer, error) {
	fn, err := procs("SherpaOnnxCreateOfflineRecognizer", "SherpaOnnxDestroyOfflineRecognizer",
		"SherpaOnnxCreateOfflineStream", "SherpaOnnxDestroyOfflineStream", "SherpaOnnxAcceptWaveformOffline",
		"SherpaOnnxDecodeOfflineStream", "SherpaOnnxGetOfflineStreamResult", "SherpaOnnxDestroyOfflineRecognizerResult")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("speech recognition model %s not installed; see `voice models`", dir)
	}
	r := &Recognizer{fn: fn}
	if set, err := procs("SherpaOnnxOfflineRecognizerSetConfig"); err == nil {
		r.setConfig = set[0]
	}
	const p = "SherpaOnnxOfflineRecognizerConfig."
	var need []string
	r.config = func(lang string) *cbuf {
		c := newCBuf("SherpaOnnxOfflineRecognizerConfig")
		c.i32(p+"feat_config.sample_rate", 16000)
		c.i32(p+"feat_config.feature_dim", 80)
		c.i32(p+"model_config.num_threads", int32(max(1, threads)))
		c.str(p+"model_config.provider", "cpu")
		c.str(p+"decoding_method", "greedy_search")
		c.i32(p+"max_active_paths", 4)
		need = need[:0]
		file := func(off, pattern string) {
			f := pick(dir, pattern)
			need = append(need, f)
			c.str(off, f)
		}
		switch {
		case has(dir, "joiner*.onnx"):
			r.Family = "transducer"
			file(p+"model_config.transducer.encoder", "encoder*.onnx")
			file(p+"model_config.transducer.decoder", "decoder*.onnx")
			file(p+"model_config.transducer.joiner", "joiner*.onnx")
			file(p+"model_config.tokens", "tokens.txt")
			c.str(p+"model_config.model_type", "nemo_transducer")
		case has(dir, "*-encoder*.onnx"):
			r.Family = "whisper"
			// Prefer the int8 files when both are present: much faster on CPU.
			enc, dec := "*-encoder.int8.onnx", "*-decoder.int8.onnx"
			if !has(dir, enc) {
				enc, dec = "*-encoder.onnx", "*-decoder.onnx"
			}
			file(p+"model_config.whisper.encoder", enc)
			file(p+"model_config.whisper.decoder", dec)
			file(p+"model_config.tokens", "*-tokens.txt")
			c.str(p+"model_config.whisper.language", lang)
			c.str(p+"model_config.whisper.task", "transcribe")
			c.i32(p+"model_config.whisper.tail_paddings", -1)
		default:
			return nil
		}
		return c
	}
	c := r.config(lang)
	if c == nil {
		return nil, fmt.Errorf("%s: unsupported speech recognition model", dir)
	}
	for _, f := range need {
		if !exists(f) {
			return nil, fmt.Errorf("missing model file %s", f)
		}
	}
	r.h = call(fn[fCreateRec], c.ptr())
	if r.h == 0 {
		return nil, fmt.Errorf("sherpa-onnx rejected the speech recognition model in %s", dir)
	}
	r.base, r.lang, r.conf = lang, lang, c
	return r, nil
}

// Transcribe returns the text and, for Whisper, the detected language.
func (r *Recognizer) Transcribe(pcm []float32, rate int) (string, string, error) {
	return r.TranscribeIn(pcm, rate, "")
}

// CanSetLanguage reports whether TranscribeIn can make Whisper recognize a
// given language instead of the one it detects.
func (r *Recognizer) CanSetLanguage() bool { return r.Family == "whisper" && r.setConfig != 0 }

// TranscribeIn is Transcribe with Whisper told the language (en, pl, ...);
// "" uses the one the recognizer was made with (or detects), as do models
// that cannot be told.
func (r *Recognizer) TranscribeIn(pcm []float32, rate int, language string) (string, string, error) {
	if len(pcm) == 0 {
		return "", "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h == 0 {
		return "", "", errors.New("recognizer closed")
	}
	// Whisper ignores the per-stream "language" option; it follows its
	// configuration, which changes in place without loading the model again.
	if language == "" {
		language = r.base
	}
	if language != r.lang && r.CanSetLanguage() {
		if c := r.config(language); c != nil {
			call(r.setConfig, r.h, c.ptr())
			r.lang, r.conf = language, c
		}
	}
	stream := call(r.fn[fCreateStream], r.h)
	if stream == 0 {
		return "", "", errors.New("cannot create recognition stream")
	}
	defer call(r.fn[fDestroyStream], stream)
	call(r.fn[fAccept], stream, uintptr(rate), uintptr(unsafe.Pointer(&pcm[0])), uintptr(len(pcm)))
	runtime.KeepAlive(pcm)
	call(r.fn[fDecode], r.h, stream)
	res := call(r.fn[fResult], stream)
	if res == 0 {
		return "", "", errors.New("recognition returned no result")
	}
	defer call(r.fn[fDestroyResult], res)
	text := cString(*(*uintptr)(ptr(res + asrLayout["SherpaOnnxOfflineRecognizerResult.text"])))
	lang := cString(*(*uintptr)(ptr(res + asrLayout["SherpaOnnxOfflineRecognizerResult.lang"])))
	return text, lang, nil
}

func (r *Recognizer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h != 0 {
		call(r.fn[fDestroyRec], r.h)
		r.h = 0
	}
}

// VAD finds speech segments with Silero VAD at 16 kHz.
type VAD struct {
	h  uintptr
	fn []uintptr
	c  *cbuf
}

// VADWindow is the number of samples AcceptWaveform expects per call.
const VADWindow = 512

const (
	fCreateVAD = iota
	fDestroyVAD
	fVADAccept
	fVADEmpty
	fVADDetected
	fVADFront
	fVADPop
	fVADFlush
	fDestroySegment
)

// NewVAD loads silero_vad.onnx. minSilence is how long a pause ends a
// segment.
func NewVAD(model string, minSilence float32) (*VAD, error) {
	fn, err := procs("SherpaOnnxCreateVoiceActivityDetector", "SherpaOnnxDestroyVoiceActivityDetector",
		"SherpaOnnxVoiceActivityDetectorAcceptWaveform", "SherpaOnnxVoiceActivityDetectorEmpty",
		"SherpaOnnxVoiceActivityDetectorDetected", "SherpaOnnxVoiceActivityDetectorFront",
		"SherpaOnnxVoiceActivityDetectorPop", "SherpaOnnxVoiceActivityDetectorFlush", "SherpaOnnxDestroySpeechSegment")
	if err != nil {
		return nil, err
	}
	if !exists(model) {
		return nil, fmt.Errorf("voice activity model %s not installed; run `voice models pull vad`", model)
	}
	c := newCBuf("SherpaOnnxVadModelConfig")
	const p = "SherpaOnnxVadModelConfig."
	c.str(p+"silero_vad.model", model)
	c.f32(p+"silero_vad.threshold", 0.5)
	c.f32(p+"silero_vad.min_silence_duration", minSilence)
	c.f32(p+"silero_vad.min_speech_duration", 0.25)
	c.i32(p+"silero_vad.window_size", VADWindow)
	c.f32(p+"silero_vad.max_speech_duration", 30)
	c.i32(p+"sample_rate", 16000)
	c.i32(p+"num_threads", 1)
	c.str(p+"provider", "cpu")
	// buffer_size_in_seconds is a float argument in second position.
	h := call(fn[fCreateVAD], c.ptr(), uintptr(math.Float32bits(120)))
	if h == 0 {
		return nil, errors.New("sherpa-onnx rejected the voice activity model")
	}
	return &VAD{h: h, fn: fn, c: c}, nil
}

// Accept feeds exactly VADWindow samples.
func (v *VAD) Accept(window []float32) {
	call(v.fn[fVADAccept], v.h, uintptr(unsafe.Pointer(&window[0])), uintptr(len(window)))
	runtime.KeepAlive(window)
}

// Speaking reports whether speech is going on right now.
func (v *VAD) Speaking() bool { return call(v.fn[fVADDetected], v.h)&0xffffffff != 0 }

// Flush closes a segment still in progress.
func (v *VAD) Flush() { call(v.fn[fVADFlush], v.h) }

// Segments pops every finished speech segment.
func (v *VAD) Segments() [][]float32 {
	var out [][]float32
	for call(v.fn[fVADEmpty], v.h)&0xffffffff == 0 {
		seg := call(v.fn[fVADFront], v.h)
		if seg != 0 {
			n := *(*int32)(ptr(seg + asrLayout["SherpaOnnxSpeechSegment.n"]))
			samples := *(*uintptr)(ptr(seg + asrLayout["SherpaOnnxSpeechSegment.samples"]))
			if n > 0 && samples != 0 {
				out = append(out, append([]float32(nil), unsafe.Slice((*float32)(ptr(samples)), n)...))
			}
			call(v.fn[fDestroySegment], seg)
		}
		call(v.fn[fVADPop], v.h)
	}
	return out
}

func (v *VAD) Close() {
	if v.h != 0 {
		call(v.fn[fDestroyVAD], v.h)
		v.h = 0
	}
	runtime.KeepAlive(v.c)
}

func has(dir, pattern string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(m) > 0
}
