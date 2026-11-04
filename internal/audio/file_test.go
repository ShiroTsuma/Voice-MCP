package audio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "sub", "hi.wav")
	if err := WriteFile(wav, []float32{0.1, -0.1, 0.2}, 24000); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(wav)
	if pcm, rate, err := DecodeWAV(b); err != nil || rate != 24000 || len(pcm) != 3 {
		t.Fatalf("read back: %d samples at %d, %v", len(pcm), rate, err)
	}
	if err := WriteFile(filepath.Join(dir, "hi.ogg"), []float32{0.1}, 24000); err == nil {
		t.Fatal("an .ogg was written")
	}
	if err := WriteFile(filepath.Join(dir, "empty.wav"), nil, 24000); err == nil {
		t.Fatal("an empty file was written")
	}
}
