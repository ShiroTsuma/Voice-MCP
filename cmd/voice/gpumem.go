package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gpuMemory is the graphics card's free and total memory in GB, the way the
// card's driver sees it: every program counts, those in WSL too. known is
// false where voice cannot tell, and then it does not check. Tests replace it.
var gpuMemory = cardMemory

// cardMemory reads Windows' own counters, which work for every card maker
// (NVIDIA, AMD, Intel); nvidia-smi is the fallback where they cannot be read.
// Off Windows neither is there and known is false.
func cardMemory(ctx context.Context) (free, total float64, known bool) {
	if free, total, known = adapterMemory(); known {
		return free, total, true
	}
	return nvidiaMemory(ctx)
}

// gpuAdapter is a graphics adapter as DXGI lists it: its LUID, which also
// names its performance counter instances, and its own memory.
type gpuAdapter struct {
	luidHigh  int32
	luidLow   uint32
	dedicated uint64 // bytes
	software  bool   // Microsoft Basic Render Driver and the like
}

// key is the start of the adapter's counter instance names,
// "luid_0x00000000_0x0000d1a3", in lower case.
func (a gpuAdapter) key() string {
	return fmt.Sprintf("luid_0x%08x_0x%08x", uint32(a.luidHigh), a.luidLow)
}

// minDedicated is the least own memory an adapter needs to be checked. An
// integrated graphics chip reports a token amount (128 MB or so) and uses
// the PC's memory instead; its "free" would say nothing.
const minDedicated = 1 << 30

// pickAdapterMemory picks the adapter the GPU voices run on, the one with
// the most own memory, and subtracts what its counter instances say is in
// use. usage maps counter instance names ("luid_0x..._0x..._phys_0", one per
// physical part of the adapter) to bytes of dedicated memory in use.
//
// DXGI may list one card more than once (an RTX 5080 shows up three times
// with three LUIDs, only one of which has counters), so among adapters of the
// same size the one with counters wins. If the largest has none, voice cannot
// tell rather than reading a smaller card.
func pickAdapterMemory(adapters []gpuAdapter, usage map[string]float64) (free, total float64, known bool) {
	usedBy := func(a gpuAdapter) (used float64, found bool) {
		prefix := a.key() + "_"
		for name, v := range usage {
			if strings.HasPrefix(strings.ToLower(name), prefix) {
				used += v
				found = true
			}
		}
		return used, found
	}
	var best gpuAdapter
	var used float64
	found, any := false, false
	for _, a := range adapters {
		if a.software || a.dedicated < minDedicated {
			continue
		}
		u, f := usedBy(a)
		if !any || a.dedicated > best.dedicated || (a.dedicated == best.dedicated && f && !found) {
			best, used, found, any = a, u, f, true
		}
	}
	if !found {
		return 0, 0, false
	}
	const gb = 1 << 30
	total = float64(best.dedicated) / gb
	free = total - used/gb
	if free < 0 {
		free = 0
	}
	return free, total, true
}

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
