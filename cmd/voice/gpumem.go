package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gpuMemory is the graphics card's free and total memory in GB, the way the
// card's driver sees it: every program counts, those in WSL too. known is
// false where voice cannot tell: without an NVIDIA card (AMD, Intel, none)
// it does not check, as before. Tests replace it.
var gpuMemory = nvidiaMemory

// nvidiaMemory asks nvidia-smi, which comes with the NVIDIA driver, about the
// first card: the one a GPU voice's server uses.
func nvidiaMemory(ctx context.Context) (free, total float64, known bool) {
	if !gpuKinds()["nvidia"] {
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=memory.free,memory.total", "--format=csv,noheader,nounits")
	hidden(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}
	return parseNvidiaMemory(string(out))
}

// parseNvidiaMemory reads "1234, 16303" (MiB, one line per card) for the
// first card.
func parseNvidiaMemory(out string) (free, total float64, known bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	f, t, ok := strings.Cut(line, ",")
	if !ok {
		return 0, 0, false
	}
	fm, err1 := strconv.ParseFloat(strings.TrimSpace(f), 64)
	tm, err2 := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err1 != nil || err2 != nil || tm <= 0 {
		return 0, 0, false
	}
	return fm / 1024, tm / 1024, true
}
