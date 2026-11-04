//go:build windows

package main

import (
	"time"

	"golang.org/x/sys/windows"
)

var procTickCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

// sinceBoot is how long Windows has been running.
func sinceBoot() time.Duration {
	ms, _, _ := procTickCount.Call()
	return time.Duration(ms) * time.Millisecond
}
