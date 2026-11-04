package audio

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WriteFile saves mono samples at rate to path: a .wav as it is, a .mp3
// through ffmpeg on PATH (128 kbit/s).
func WriteFile(path string, pcm []float32, rate int) error {
	if len(pcm) == 0 {
		return errors.New("no audio to save")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	wav := EncodeWAV(pcm, rate)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		return os.WriteFile(path, wav, 0o644)
	case ".mp3":
		ffmpeg, err := exec.LookPath("ffmpeg")
		if err != nil {
			return errors.New("an .mp3 needs ffmpeg on PATH; save a .wav instead")
		}
		var stderr bytes.Buffer
		cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "wav", "-i", "-", "-codec:a", "libmp3lame", "-b:a", "128k", path)
		cmd.Stdin, cmd.Stderr = bytes.NewReader(wav), &stderr
		hideConsole(cmd)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	return fmt.Errorf("%s: save as .wav or .mp3", filepath.Base(path))
}
