package sherpa

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// kokoroV1Speakers is the speaker order of kokoro-multi-lang-v1_0 (sid =
// index), used when the model metadata cannot be read. The first letter of a
// name is the language, the second the gender.
var kokoroV1Speakers = []string{
	"af_alloy", "af_aoede", "af_bella", "af_heart", "af_jessica", "af_kore", "af_nicole", "af_nova", "af_river", "af_sarah", "af_sky",
	"am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael", "am_onyx", "am_puck", "am_santa",
	"bf_alice", "bf_emma", "bf_isabella", "bf_lily",
	"bm_daniel", "bm_fable", "bm_george", "bm_lewis",
	"ef_dora", "em_alex", "ff_siwis", "hf_alpha", "hf_beta", "hm_omega", "hm_psi",
	"if_sara", "im_nicola", "jf_alpha", "jf_gongitsune", "jf_nezumi", "jf_tebukuro", "jm_kumo",
	"pf_dora", "pm_alex", "pm_santa",
	"zf_xiaobei", "zf_xiaoni", "zf_xiaoxiao", "zf_xiaoyi", "zm_yunjian", "zm_yunxi", "zm_yunxia", "zm_yunyang",
	"em_santa",
}

// kokoroSpeakers returns voice names in sid order, from the model's
// speaker_names metadata when present.
func kokoroSpeakers(dir string) []string {
	if names := onnxMetadata(filepath.Join(dir, "model.onnx"), "speaker_names"); names != "" {
		return strings.Split(names, ",")
	}
	if strings.Contains(filepath.Base(dir), "multi-lang-v1_0") {
		return append([]string(nil), kokoroV1Speakers...)
	}
	return nil
}

// onnxMetadata finds a metadata_props value without a protobuf dependency:
// ONNX writes metadata after the graph, so the tail of the file holds a
// StringStringEntryProto {1: key, 2: value} for each entry.
func onnxMetadata(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const tail = 4 << 20
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		f.Seek(-tail, io.SeekEnd)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	needle := append([]byte{0x0a, byte(len(key))}, key...)
	i := bytes.LastIndex(b, needle)
	if i < 0 {
		return ""
	}
	rest := b[i+len(needle):]
	if len(rest) < 2 || rest[0] != 0x12 {
		return ""
	}
	n, w := binary.Uvarint(rest[1:])
	if w <= 0 || uint64(len(rest)-1-w) < n {
		return ""
	}
	return string(rest[1+w : 1+w+int(n)])
}

// espeakVoice maps a Kokoro language to the espeak-ng voice used for words
// missing from the lexicon. espeak-ng-data names British English plain "en".
func espeakVoice(lang string) string {
	if lang == "en-gb" {
		return "en"
	}
	return lang
}

var kokoroLangs = map[byte]string{'a': "en", 'b': "en", 'e': "es", 'f': "fr", 'h': "hi", 'i': "it", 'j': "ja", 'p': "pt", 'z': "zh"}

func kokoroLang(name, fallback string) string {
	if len(name) > 2 && name[2] == '_' {
		if l, ok := kokoroLangs[name[0]]; ok {
			return l
		}
	}
	return fallback
}

func kokoroGender(name string) string {
	if len(name) > 2 && name[2] == '_' {
		switch name[1] {
		case 'f':
			return "female"
		case 'm':
			return "male"
		}
	}
	return ""
}
