//go:build !windows

package main

// killUnder: only Windows refuses to replace a running program.
func killUnder(dir string) int { return 0 }
