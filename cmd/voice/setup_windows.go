package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// killUnder ends every process whose program lies in dir (voice serve,
// voice mcp, its shared-mcp gateway) except this one, and returns how many.
func killUnder(dir string) int {
	prefix := strings.ToLower(filepath.Clean(dir)) + `\`
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	self := uint32(os.Getpid())
	killed := 0
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == self || e.ProcessID == 0 {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil &&
			strings.HasPrefix(strings.ToLower(windows.UTF16ToString(buf[:n])), prefix) &&
			windows.TerminateProcess(h, 1) == nil {
			windows.WaitForSingleObject(h, 5000)
			killed++
		}
		windows.CloseHandle(h)
	}
	if killed > 0 {
		// Let Windows release the image files before the folder moves.
		time.Sleep(300 * time.Millisecond)
	}
	return killed
}
