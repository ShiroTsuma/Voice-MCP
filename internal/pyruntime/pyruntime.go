// Package pyruntime is the Python voice can bring along for its GPU
// backends, so the user needs no Python of their own: a relocatable CPython
// build from python-build-standalone, pinned by version and SHA-256,
// unpacked into <sidecar_dir>\python.
package pyruntime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The pinned build. A newer one is a change here: version, URL and hash.
const (
	Version = "3.12.15"
	URL     = "https://github.com/astral-sh/python-build-standalone/releases/download/20261013/cpython-3.12.15%2B20261013-x86_64-pc-windows-msvc-install_only.tar.gz"
	SHA256  = "bbc902d4d187ac4914915bf538196a064b49109a33a104965b5164ced5b80a1e"
	SizeMB  = 45
)

// Dir is where the runtime goes under the sidecar folder.
func Dir(sidecarDir string) string { return filepath.Join(sidecarDir, "python") }

// Exe is its python.exe.
func Exe(sidecarDir string) string { return filepath.Join(Dir(sidecarDir), "python.exe") }

// Installed reports whether the runtime is unpacked.
func Installed(sidecarDir string) bool {
	_, err := os.Stat(Exe(sidecarDir))
	return err == nil
}

// Install downloads the pinned build, checks its hash and unpacks it into
// Dir, replacing what was there. progress gets the bytes so far.
func Install(ctx context.Context, sidecarDir string, progress func(done, total int64)) error {
	return install(ctx, sidecarDir, URL, SHA256, progress)
}

func install(ctx context.Context, sidecarDir, url, want string, progress func(done, total int64)) error {
	if err := os.MkdirAll(sidecarDir, 0o755); err != nil {
		return err
	}
	archive, err := os.CreateTemp(sidecarDir, "python-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()

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
		return fmt.Errorf("downloading Python: %s", resp.Status)
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			archive.Write(buf[:n])
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("the Python download does not match its pinned SHA-256 (got %s); nothing was installed", got)
	}

	// Unpack beside the target, then swap it in: a failed unpack leaves the old one.
	stage := Dir(sidecarDir) + ".new"
	os.RemoveAll(stage)
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := untar(archive, stage); err != nil {
		os.RemoveAll(stage)
		return err
	}
	if _, err := os.Stat(filepath.Join(stage, "python.exe")); err != nil {
		os.RemoveAll(stage)
		return errors.New("the Python archive has no python.exe")
	}
	os.RemoveAll(Dir(sidecarDir))
	return os.Rename(stage, Dir(sidecarDir))
}

// untar unpacks the archive's python/ folder into dir.
func untar(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	root, _ := filepath.Abs(dir)
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(filepath.ToSlash(hd.Name), "python/")
		if name == "" || name == hd.Name && !strings.HasPrefix(hd.Name, "python") {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if !strings.HasPrefix(target, root+string(filepath.Separator)) && target != root {
			return fmt.Errorf("unsafe path in the Python archive: %s", hd.Name)
		}
		switch hd.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}
