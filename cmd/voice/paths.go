package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"voice/internal/audio"
	"voice/internal/clones"
	"voice/internal/core"
	"voice/internal/engine"
	"voice/internal/engine/httpengine"
)

// runPaths shows where voice keeps its data, so it can be moved off a full
// system drive with data_root or the *_dir settings.
func runPaths(args []string) error {
	fs, cfgPath := flags("paths")
	fs.Parse(args)
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	fmt.Printf("config       %s\n", *cfgPath)
	for _, p := range []struct{ key, dir string }{
		{"models_dir", engine.Dirs.Models},
		{"runtime_dir", engine.Dirs.Runtime},
		{"voices_dir", clones.Dir},
		{"state_dir", core.DataDir()},
		{"sidecar_dir", httpengine.SidecarDir},
	} {
		fmt.Printf("%-12s %s  (%s)\n", p.key, p.dir, size(p.dir))
	}
	return nil
}

func size(dir string) string {
	var total int64
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	switch {
	case total >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(total)/(1<<30))
	case total >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(total)/(1<<20))
	}
	return fmt.Sprintf("%d KB", total>>10)
}

// runMixer lists Windows audio sessions (volume mixer entries).
func runMixer([]string) error {
	s, err := audio.NewSessions()
	if err != nil {
		return err
	}
	for _, info := range s.List() {
		name := info.Name
		state := "inactive"
		if info.Active {
			state = "active"
		}
		fmt.Printf("pid %-6d %-8s volume %.2f  %s\n", info.PID, state, info.Volume, name)
	}
	return nil
}

// runSidecar installs the Python sidecar into the configured sidecar_dir.
const sidecarUsage = `usage: voice sidecar setup [flags] chatterbox|f5|xtts|test[,...]

Installs GPU voice backends into the sidecar dir (voice paths shows it), each in
its own Python environment. Reruns resume: downloads are cached and finished
stages are skipped.

flags:
  -gpu auto|nvidia|amd|cpu   PyTorch build (auto detects the card)
  -cuda cu128                CUDA build for NVIDIA
  -profile NAME|FILE         AMD wheel set from sidecar\profiles (default amd-rocm-windows)
  -torch-wheels URL,...      explicit PyTorch wheel URLs instead of a profile
  -python PATH               python.exe to use (default: Python 3.12 found automatically)
  -force                     redo finished stages`

func runSidecar(args []string) error {
	// Help changes nothing, wherever it appears.
	for _, a := range args {
		if a == "-h" || a == "-help" || a == "--help" || a == "help" {
			fmt.Println(sidecarUsage)
			return nil
		}
	}
	fs, cfgPath := flags("sidecar")
	gpu := fs.String("gpu", "auto", "auto, nvidia, amd or cpu")
	cuda := fs.String("cuda", "cu128", "CUDA build for NVIDIA: cu128, cu126")
	profile := fs.String("profile", "", "AMD wheel profile name or file")
	wheels := fs.String("torch-wheels", "", "comma-separated PyTorch wheel URLs")
	python := fs.String("python", "", "python.exe to use")
	force := fs.Bool("force", false, "redo finished stages")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 2 || rest[0] != "setup" {
		return errors.New(sidecarUsage)
	}
	var backends []string
	for _, b := range strings.Split(strings.Join(rest[1:], ","), ",") {
		switch b = strings.TrimSpace(b); b {
		case "":
		case "chatterbox", "f5", "xtts", "test":
			backends = append(backends, b)
		default:
			return fmt.Errorf("unknown backend %q\n%s", b, sidecarUsage)
		}
	}
	switch *gpu {
	case "auto", "nvidia", "amd", "cpu":
	default:
		return fmt.Errorf("unknown -gpu %q\n%s", *gpu, sidecarUsage)
	}
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	script := filepath.Join(httpengine.SidecarCode, "setup.ps1")
	ps := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
		"-Backend", strings.Join(backends, ","), "-Gpu", *gpu, "-Cuda", *cuda, "-Target", httpengine.SidecarDir}
	if *wheels != "" {
		ps = append(ps, "-TorchWheels", *wheels)
	}
	if *profile != "" {
		ps = append(ps, "-TorchProfile", *profile)
	}
	if *python != "" {
		ps = append(ps, "-Python", *python)
	}
	if *force {
		ps = append(ps, "-Force")
	}
	cmd := exec.Command("powershell", ps...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}
