//go:build !windows

package main

// gpuKinds is unknown off Windows: every GPU voice is offered.
func gpuKinds() map[string]bool { return nil }

// gpuTotalGB is unknown off Windows.
func gpuTotalGB() float64 { return 0 }

// adapterMemory is unknown off Windows: there are no graphics counters to read.
func adapterMemory() (free, total float64, known bool) { return 0, 0, false }
