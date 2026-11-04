// Package sherpa runs Kokoro and Piper/VITS models through the sherpa-onnx
// C API, loaded from DLLs at runtime so the binary needs no cgo and a missing
// runtime only disables this engine.
package sherpa

// Version is the sherpa-onnx release these structs mirror. The 2.x line
// dropped built-in espeak-ng phonemization, which Piper and Kokoro rely on.
const Version = "1.13.8"

// Go mirrors of the C structs in testdata/c-api.h. Field order and types
// must match exactly; TestLayoutMatchesHeader compiles the header with gcc
// and compares every offset. Strings are uintptr to C strings whose backing
// memory the engine keeps alive.

type cVits struct {
	Model, Lexicon, Tokens, DataDir      uintptr
	NoiseScale, NoiseScaleW, LengthScale float32
	DictDir                              uintptr
}

type cMatcha struct {
	AcousticModel, Vocoder, Lexicon, Tokens, DataDir uintptr
	NoiseScale, LengthScale                          float32
	DictDir                                          uintptr
}

type cKokoro struct {
	Model, Voices, Tokens, DataDir uintptr
	LengthScale                    float32
	DictDir, Lexicon, Lang         uintptr
}

type cKitten struct {
	Model, Voices, Tokens, DataDir uintptr
	LengthScale                    float32
}

type cZipvoice struct {
	Tokens, Encoder, Decoder, Vocoder, DataDir, Lexicon uintptr
	FeatScale, TShift, TargetRMS, GuidanceScale         float32
}

type cPocket struct {
	LmFlow, LmMain, Encoder, Decoder, TextConditioner, VocabJSON, TokenScoresJSON uintptr
	VoiceEmbeddingCacheCapacity                                                   int32
}

type cSupertonic struct {
	DurationPredictor, TextEncoder, VectorEstimator, Vocoder, TTSJSON, UnicodeIndexer, VoiceStyle uintptr
}

type cModelConfig struct {
	Vits              cVits
	NumThreads, Debug int32
	Provider          uintptr
	Matcha            cMatcha
	Kokoro            cKokoro
	Kitten            cKitten
	Zipvoice          cZipvoice
	Pocket            cPocket
	Supertonic        cSupertonic
}

type cTTSConfig struct {
	Model           cModelConfig
	RuleFsts        uintptr
	MaxNumSentences int32
	RuleFars        uintptr
	SilenceScale    float32
}

type cGenerationConfig struct {
	SilenceScale, Speed                    float32
	Sid                                    int32
	ReferenceAudio                         uintptr
	ReferenceAudioLen, ReferenceSampleRate int32
	ReferenceText                          uintptr
	NumSteps                               int32
	Extra                                  uintptr
}

type cGeneratedAudio struct {
	Samples       uintptr
	N, SampleRate int32
}

// The recognizer and VAD configs are large structs of which voice sets a
// few fields. They are passed as zeroed byte buffers with those fields
// written at offsets from testdata/c-api.h; TestLayoutMatchesHeader
// recompiles the header and checks every offset below.
var asrLayout = map[string]uintptr{
	"SherpaOnnxOfflineRecognizerConfig":                                    608,
	"SherpaOnnxOfflineRecognizerConfig.feat_config.sample_rate":            0,
	"SherpaOnnxOfflineRecognizerConfig.feat_config.feature_dim":            4,
	"SherpaOnnxOfflineRecognizerConfig.model_config.transducer.encoder":    8,
	"SherpaOnnxOfflineRecognizerConfig.model_config.transducer.decoder":    16,
	"SherpaOnnxOfflineRecognizerConfig.model_config.transducer.joiner":     24,
	"SherpaOnnxOfflineRecognizerConfig.model_config.whisper.encoder":       48,
	"SherpaOnnxOfflineRecognizerConfig.model_config.whisper.decoder":       56,
	"SherpaOnnxOfflineRecognizerConfig.model_config.whisper.language":      64,
	"SherpaOnnxOfflineRecognizerConfig.model_config.whisper.task":          72,
	"SherpaOnnxOfflineRecognizerConfig.model_config.whisper.tail_paddings": 80,
	"SherpaOnnxOfflineRecognizerConfig.model_config.tokens":                104,
	"SherpaOnnxOfflineRecognizerConfig.model_config.num_threads":           112,
	"SherpaOnnxOfflineRecognizerConfig.model_config.debug":                 116,
	"SherpaOnnxOfflineRecognizerConfig.model_config.provider":              120,
	"SherpaOnnxOfflineRecognizerConfig.model_config.model_type":            128,
	"SherpaOnnxOfflineRecognizerConfig.decoding_method":                    528,
	"SherpaOnnxOfflineRecognizerConfig.max_active_paths":                   536,
	"SherpaOnnxOfflineRecognizerResult.text":                               0,
	"SherpaOnnxOfflineRecognizerResult.lang":                               48,
	"SherpaOnnxVadModelConfig":                                             88,
	"SherpaOnnxVadModelConfig.silero_vad.model":                            0,
	"SherpaOnnxVadModelConfig.silero_vad.threshold":                        8,
	"SherpaOnnxVadModelConfig.silero_vad.min_silence_duration":             12,
	"SherpaOnnxVadModelConfig.silero_vad.min_speech_duration":              16,
	"SherpaOnnxVadModelConfig.silero_vad.window_size":                      20,
	"SherpaOnnxVadModelConfig.silero_vad.max_speech_duration":              24,
	"SherpaOnnxVadModelConfig.sample_rate":                                 32,
	"SherpaOnnxVadModelConfig.num_threads":                                 36,
	"SherpaOnnxVadModelConfig.provider":                                    40,
	"SherpaOnnxVadModelConfig.debug":                                       48,
	"SherpaOnnxSpeechSegment":                                              24,
	"SherpaOnnxSpeechSegment.start":                                        0,
	"SherpaOnnxSpeechSegment.samples":                                      8,
	"SherpaOnnxSpeechSegment.n":                                            16,
}
