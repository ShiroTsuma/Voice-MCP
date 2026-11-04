package sherpa

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// cStructs pairs each C struct with its Go mirror and the C field names in
// declaration order.
var cStructs = []struct {
	name   string
	goType any
	fields []string
}{
	{"SherpaOnnxOfflineTtsVitsModelConfig", cVits{}, []string{"model", "lexicon", "tokens", "data_dir", "noise_scale", "noise_scale_w", "length_scale", "dict_dir"}},
	{"SherpaOnnxOfflineTtsMatchaModelConfig", cMatcha{}, []string{"acoustic_model", "vocoder", "lexicon", "tokens", "data_dir", "noise_scale", "length_scale", "dict_dir"}},
	{"SherpaOnnxOfflineTtsKokoroModelConfig", cKokoro{}, []string{"model", "voices", "tokens", "data_dir", "length_scale", "dict_dir", "lexicon", "lang"}},
	{"SherpaOnnxOfflineTtsKittenModelConfig", cKitten{}, []string{"model", "voices", "tokens", "data_dir", "length_scale"}},
	{"SherpaOnnxOfflineTtsZipvoiceModelConfig", cZipvoice{}, []string{"tokens", "encoder", "decoder", "vocoder", "data_dir", "lexicon", "feat_scale", "t_shift", "target_rms", "guidance_scale"}},
	{"SherpaOnnxOfflineTtsPocketModelConfig", cPocket{}, []string{"lm_flow", "lm_main", "encoder", "decoder", "text_conditioner", "vocab_json", "token_scores_json", "voice_embedding_cache_capacity"}},
	{"SherpaOnnxOfflineTtsSupertonicModelConfig", cSupertonic{}, []string{"duration_predictor", "text_encoder", "vector_estimator", "vocoder", "tts_json", "unicode_indexer", "voice_style"}},
	{"SherpaOnnxOfflineTtsModelConfig", cModelConfig{}, []string{"vits", "num_threads", "debug", "provider", "matcha", "kokoro", "kitten", "zipvoice", "pocket", "supertonic"}},
	{"SherpaOnnxOfflineTtsConfig", cTTSConfig{}, []string{"model", "rule_fsts", "max_num_sentences", "rule_fars", "silence_scale"}},
	{"SherpaOnnxGenerationConfig", cGenerationConfig{}, []string{"silence_scale", "speed", "sid", "reference_audio", "reference_audio_len", "reference_sample_rate", "reference_text", "num_steps", "extra"}},
	{"SherpaOnnxGeneratedAudio", cGeneratedAudio{}, []string{"samples", "n", "sample_rate"}},
}

// TestLayoutMatchesHeader compiles the vendored header with gcc and checks
// every struct size and field offset against the Go mirrors. It is skipped
// when gcc is not available (the product build never needs it).
func TestLayoutMatchesHeader(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		if p := filepath.Join("..", "..", "..", "..", "resources", "mingw64", "bin", "gcc.exe"); fileExists(p) {
			gcc = p
		} else {
			t.Skip("gcc not found; layout not verified")
		}
	}
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("#include <stdio.h>\n#include <stddef.h>\n#include \"c-api.h\"\nint main(void) {\n")
	for _, s := range cStructs {
		fmt.Fprintf(&src, "printf(\"%s %%zu\\n\", sizeof(%s));\n", s.name, s.name)
		for _, f := range s.fields {
			fmt.Fprintf(&src, "printf(\"%s.%s %%zu\\n\", offsetof(%s, %s));\n", s.name, f, s.name, f)
		}
	}
	for key := range asrLayout {
		typ, field, nested := strings.Cut(key, ".")
		if nested {
			fmt.Fprintf(&src, "printf(\"%s %%zu\\n\", offsetof(%s, %s));\n", key, typ, field)
		} else {
			fmt.Fprintf(&src, "printf(\"%s %%zu\\n\", sizeof(%s));\n", key, typ)
		}
	}
	src.WriteString("return 0;\n}\n")
	cFile := filepath.Join(dir, "layout.c")
	if err := os.WriteFile(cFile, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	header, _ := filepath.Abs("testdata")
	exe := filepath.Join(dir, "layout.exe")
	if out, err := exec.Command(gcc, "-I", header, "-o", exe, cFile).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	out, err := exec.Command(exe).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uintptr{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		want[k] = uintptr(n)
	}
	for key, off := range asrLayout {
		if got, ok := want[key]; !ok || got != off {
			t.Errorf("asrLayout[%s] = %d, C says %d", key, off, got)
		}
	}
	for _, s := range cStructs {
		typ := reflect.TypeOf(s.goType)
		if typ.Size() != want[s.name] {
			t.Errorf("%s: Go size %d, C size %d", s.name, typ.Size(), want[s.name])
		}
		if typ.NumField() != len(s.fields) {
			t.Errorf("%s: Go has %d fields, C has %d", s.name, typ.NumField(), len(s.fields))
			continue
		}
		for i, f := range s.fields {
			if got, exp := typ.Field(i).Offset, want[s.name+"."+f]; got != exp {
				t.Errorf("%s.%s: Go offset %d, C offset %d", s.name, f, got, exp)
			}
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
