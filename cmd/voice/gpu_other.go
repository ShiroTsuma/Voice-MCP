//go:build !windows

package main

// gpuKinds is unknown off Windows: every GPU voice is offered.
func gpuKinds() map[string]bool { return nil }

// gpuTotalGB is unknown off Windows.
func gpuTotalGB() float64 { return 0 }
