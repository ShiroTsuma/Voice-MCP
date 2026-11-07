// Package models downloads the sherpa-onnx runtime and TTS models.
package models

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	runtimeURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-win-x64-shared-MT-Release.tar.bz2"
	modelsURL  = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/"
)

type Item struct {
	Name        string
	Description string
	URL         string
	SizeMB      int
	Runtime     bool              // extract DLLs into the runtime dir instead of a model dir
	Dir         string            // directory name under the models dir
	Extra       map[string]string // extra files to fetch into the model dir: name -> URL
}

// Catalog lists what `voice models pull` can fetch. The MT runtime build
// links the C runtime statically, so no Visual C++ redistributable is needed.
var Catalog = []Item{
	{Name: "runtime", Description: "sherpa-onnx 1.13.8 native runtime (needed by all neural voices)", URL: runtimeURL, SizeMB: 25, Runtime: true},
	{Name: "kokoro", Description: "Kokoro 82M multi-language, 54 voices (English, best quality)", URL: modelsURL + "kokoro-multi-lang-v1_0.tar.bz2", SizeMB: 334, Dir: "kokoro-multi-lang-v1_0"},
	{Name: "pocket", Description: "Pocket TTS voice cloning from a short clip, English (non-commercial license)", URL: modelsURL + "sherpa-onnx-pocket-tts-2026-01-26.tar.bz2", SizeMB: 153, Dir: "sherpa-onnx-pocket-tts-2026-01-26"},
	{Name: "zipvoice", Description: "ZipVoice voice cloning from a clip and its transcript, English/Chinese", URL: modelsURL + "sherpa-onnx-zipvoice-distill-int8-zh-en-emilia.tar.bz2", SizeMB: 99, Dir: "sherpa-onnx-zipvoice-distill-int8-zh-en-emilia",
		Extra: map[string]string{"vocos_24khz.onnx": "https://github.com/k2-fsa/sherpa-onnx/releases/download/vocoder-models/vocos_24khz.onnx"}},
	{Name: "asr-parakeet", Description: "Parakeet TDT v3 speech recognition, 25 European languages incl. English and Polish", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8.tar.bz2", SizeMB: 465, Dir: "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8"},
	{Name: "asr-whisper", Description: "Whisper large-v3 turbo speech recognition, ~100 languages", URL: "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-turbo.tar.bz2", SizeMB: 538, Dir: "sherpa-onnx-whisper-turbo"},
	{Name: "vad", Description: "Silero voice activity detection (needed for listening)", SizeMB: 2, Dir: "silero-vad",
		Extra: map[string]string{"silero_vad.onnx": "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad_v5.onnx"}},
	{Name: "supertonic3", Description: "Supertonic 3, 10 voices in 31 languages, fast on the processor (OpenRAIL-M weights)", URL: modelsURL + "sherpa-onnx-supertonic-3-tts-int8-2026-05-11.tar.bz2", SizeMB: 123, Dir: "sherpa-onnx-supertonic-3-tts-int8-2026-05-11"},
	{Name: "piper-pl-gosia", Description: "Piper Polish female (gosia)", URL: modelsURL + "vits-piper-pl_PL-gosia-medium.tar.bz2", SizeMB: 64, Dir: "vits-piper-pl_PL-gosia-medium"},
	{Name: "piper-pl-justyna", Description: "Piper Polish female (justyna)", URL: modelsURL + "vits-piper-pl_PL-justyna_wg_glos-medium.tar.bz2", SizeMB: 64, Dir: "vits-piper-pl_PL-justyna_wg_glos-medium"},
	{Name: "piper-pl-darkman", Description: "Piper Polish male (darkman)", URL: modelsURL + "vits-piper-pl_PL-darkman-medium.tar.bz2", SizeMB: 64, Dir: "vits-piper-pl_PL-darkman-medium"},
}

func Find(name string) (Item, bool) {
	for _, it := range Catalog {
		if it.Name == name {
			return it, true
		}
	}
	return Item{}, false
}

// Installed reports whether an item is present in modelsDir/runtimeDir.
func Installed(it Item, modelsDir, runtimeDir string) bool {
	if it.Runtime {
		_, err := os.Stat(filepath.Join(runtimeDir, "sherpa-onnx-c-api.dll"))
		return err == nil
	}
	_, err := os.Stat(filepath.Join(modelsDir, it.Dir))
	return err == nil
}

// Pull downloads and extracts one item. Extraction goes to a staging
// directory that is renamed into place, so an interrupted pull leaves
// nothing half-installed.
func Pull(ctx context.Context, it Item, modelsDir, runtimeDir string, progress func(done, total int64)) error {
	dest := filepath.Join(modelsDir, it.Dir)
	if it.Runtime {
		dest = runtimeDir
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".pull-"+it.Name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	src := stage
	if it.URL != "" {
		if err := fetchArchive(ctx, it, stage, progress); err != nil {
			return err
		}
		if !it.Runtime {
			// Model archives hold one top-level directory named like the model.
			src = filepath.Join(stage, it.Dir)
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("archive for %s has no %s directory", it.Name, it.Dir)
			}
		}
	}
	for name, url := range it.Extra {
		if err := download(ctx, url, filepath.Join(src, name)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(src, dest)
}

func fetchArchive(ctx context.Context, it Item, stage string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s", it.URL, resp.Status)
	}
	body := &countingReader{r: resp.Body, total: resp.ContentLength, progress: progress}
	if err := extract(bzip2.NewReader(body), stage, it.Runtime); err != nil {
		return fmt.Errorf("extract %s: %w", it.Name, err)
	}
	return nil
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// extract unpacks a tar stream into dir. With dllsOnly only *.dll files from
// the archive's lib directory are kept, flattened.
func extract(r io.Reader, dir string, dllsOnly bool) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.ReplaceAll(h.Name, `\`, "/"))
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		if dllsOnly {
			if h.Typeflag != tar.TypeReg || path.Base(path.Dir(name)) != "lib" || !strings.HasSuffix(name, ".dll") {
				continue
			}
			name = path.Base(name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		}
	}
}

type countingReader struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil {
		c.progress(c.done, c.total)
	}
	return n, err
}
