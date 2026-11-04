package pyruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstall(t *testing.T) {
	good := archive(t, map[string]string{"python/python.exe": "exe", "python/Lib/os.py": "#"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(good) }))
	defer srv.Close()
	sum := sha256.Sum256(good)
	dir := t.TempDir()

	if err := install(context.Background(), dir, srv.URL, strings.Repeat("0", 64), nil); err == nil || Installed(dir) {
		t.Fatalf("a wrong hash installed: %v", err)
	}
	if err := install(context.Background(), dir, srv.URL, hex.EncodeToString(sum[:]), nil); err != nil {
		t.Fatal(err)
	}
	if !Installed(dir) {
		t.Fatal("python.exe missing after install")
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(dir), "Lib", "os.py")); string(b) != "#" {
		t.Fatal("the python/ prefix was not stripped")
	}
}

func TestUntarRefusesEscapes(t *testing.T) {
	bad := archive(t, map[string]string{"python/../../evil.txt": "x"})
	if err := untar(bytes.NewReader(bad), t.TempDir()); err == nil {
		t.Fatal("a path outside the folder was unpacked")
	}
}

// TestRealDownload checks the pinned URL and hash against the real file.
// It downloads 45 MB, so it runs only with VOICE_NET_TEST=1.
func TestRealDownload(t *testing.T) {
	if os.Getenv("VOICE_NET_TEST") != "1" {
		t.Skip("set VOICE_NET_TEST=1 to download the pinned Python")
	}
	dir := t.TempDir()
	if err := Install(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if !Installed(dir) {
		t.Fatal("no python.exe")
	}
	if _, err := os.Stat(filepath.Join(Dir(dir), "Lib", "venv")); err != nil {
		t.Fatal("the build has no venv module: the backends need it")
	}
}
