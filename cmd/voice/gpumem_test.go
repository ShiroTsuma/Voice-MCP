package main

import (
	"math"
	"testing"
)

func TestPickAdapterMemory(t *testing.T) {
	const gib = 1 << 30
	nvidia := gpuAdapter{luidHigh: 0, luidLow: 0xD1A3, dedicated: 16 * gib}
	intel := gpuAdapter{luidHigh: 0, luidLow: 0x1234, dedicated: 128 << 20}
	amd := gpuAdapter{luidHigh: 1, luidLow: 0xBEEF, dedicated: 8 * gib}
	basic := gpuAdapter{luidHigh: 0, luidLow: 0x99, dedicated: 32 * gib, software: true}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

	cases := []struct {
		name        string
		adapters    []gpuAdapter
		usage       map[string]float64
		free, total float64
		known       bool
	}{
		{"the card with most memory, counter names in upper case",
			[]gpuAdapter{intel, amd, nvidia, basic},
			map[string]float64{
				"luid_0x00000000_0x0000D1A3_phys_0": 10 * gib,
				"luid_0x00000001_0x0000BEEF_phys_0": 1 * gib,
				"luid_0x00000000_0x00001234_phys_0": 0.1 * gib,
			}, 6, 16, true},
		{"linked parts add up",
			[]gpuAdapter{amd},
			map[string]float64{
				"luid_0x00000001_0x0000beef_phys_0": 2 * gib,
				"luid_0x00000001_0x0000beef_phys_1": 1 * gib,
			}, 5, 8, true},
		{"the same card listed thrice: the one with counters",
			[]gpuAdapter{
				{luidLow: 0x62EA4, dedicated: 16 * gib},
				{luidLow: 0x12E80, dedicated: 16 * gib},
				{luidLow: 0x4DC21, dedicated: 16 * gib},
			},
			map[string]float64{"luid_0x00000000_0x00012E80_phys_0": 4 * gib}, 12, 16, true},
		{"usage past the size is none free",
			[]gpuAdapter{amd},
			map[string]float64{"luid_0x00000001_0x0000beef_phys_0": 9 * gib}, 0, 8, true},
		{"only an integrated chip: unknown",
			[]gpuAdapter{intel, basic},
			map[string]float64{"luid_0x00000000_0x00001234_phys_0": 0}, 0, 0, false},
		{"no counter for the card: unknown",
			[]gpuAdapter{nvidia},
			map[string]float64{"luid_0x00000001_0x0000beef_phys_0": gib}, 0, 0, false},
		{"no adapters", nil, nil, 0, 0, false},
	}
	for _, c := range cases {
		free, total, known := pickAdapterMemory(c.adapters, c.usage)
		if known != c.known || !near(free, c.free) || !near(total, c.total) {
			t.Errorf("%s: got %.3f of %.3f known=%v, want %.3f of %.3f known=%v", c.name, free, total, known, c.free, c.total, c.known)
		}
	}
}

func TestAdapterKey(t *testing.T) {
	a := gpuAdapter{luidHigh: -1, luidLow: 0xABC}
	if got := a.key(); got != "luid_0xffffffff_0x00000abc" {
		t.Errorf("key = %q", got)
	}
}
