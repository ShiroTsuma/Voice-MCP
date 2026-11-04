package inject

import (
	"errors"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetWindow                = user32.NewProc("GetWindow")
	procGetWindowTextLength      = user32.NewProc("GetWindowTextLengthW")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetAsyncKeyState         = user32.NewProc("GetAsyncKeyState")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
)

// Ancestors lists the parent, grandparent, ... of process pid (up to 8).
func Ancestors(pid uint32) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	parent := map[uint32]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parent[e.ProcessID] = e.ParentProcessID
	}
	var out []uint32
	for p, ok := parent[pid]; ok && p != 0 && len(out) < 8; p, ok = parent[p] {
		out = append(out, p)
	}
	return out
}

// HostApp is the nearest ancestor of pid whose executable is one of apps
// (lower-case file names): the desktop app an agent process runs inside.
func HostApp(pid uint32, apps map[string]bool) (uint32, string) {
	for _, a := range Ancestors(pid) {
		if name := strings.ToLower(ProcessName(a)); apps[name] {
			return a, name
		}
	}
	return 0, ""
}

// The window search runs through one callback made once: Go never frees a
// callback, and a long-running process may only make a limited number.
var (
	enumMu   sync.Mutex
	enumPID  uint32
	enumBest uintptr
	enumArea int32
	enumCb   = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != enumPID {
			return 1
		}
		const gwOwner = 4
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if o, _, _ := procGetWindow.Call(hwnd, gwOwner); o != 0 {
			return 1 // a dialog or tool window
		}
		if n, _, _ := procGetWindowTextLength.Call(hwnd); n == 0 {
			return 1
		}
		var r struct{ Left, Top, Right, Bottom int32 }
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		if area := (r.Right - r.Left) * (r.Bottom - r.Top); area > enumArea {
			enumBest, enumArea = hwnd, area
		}
		return 1
	})
)

// MainWindow is the largest visible, titled top-level window of process pid.
func MainWindow(pid uint32) uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPID, enumBest, enumArea = pid, 0, 0
	procEnumWindows.Call(enumCb, 0)
	return enumBest
}

// Focus brings hwnd to the front. Windows lets a process do that while it
// handles the user's input (the dictation hotkey), which is when voice calls it.
func Focus(hwnd uintptr) error {
	const swRestore = 9
	if min, _, _ := procIsIconic.Call(hwnd); min != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	procAllowSetForegroundWindow.Call(uintptr(windows.GetCurrentProcessId()))
	for i := 0; i < 3; i++ {
		procSetForegroundWindow.Call(hwnd)
		time.Sleep(100 * time.Millisecond)
		if now, _ := Foreground(); now == hwnd {
			return nil
		}
		// A tap of Alt counts as input and lifts the foreground lock.
		sendInput([]keyboardInput{{Type: inputKbd, VK: vkMenu}, {Type: inputKbd, VK: vkMenu, Flags: kfKeyUp}})
	}
	return errors.New("Windows did not let voice bring the window to the front")
}

const vkMenu = 0x12

// waitModifiersUp waits (up to 3 s) until Ctrl, Alt, Shift and Win are up:
// typed while the user still holds the dictation hotkey, the text would
// arrive as Ctrl+Alt+letter shortcuts.
func waitModifiersUp() {
	keys := []uintptr{0x10, 0x11, 0x12, 0x5B, 0x5C} // Shift, Ctrl, Alt, left and right Win
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		held := false
		for _, k := range keys {
			if s, _, _ := procGetAsyncKeyState.Call(k); s&0x8000 != 0 {
				held = true
			}
		}
		if !held {
			return
		}
	}
}

// Identity tells process pid from a later process with the same PID: when it
// started (in 100 ns units) and its executable; ok is false when it is gone.
func Identity(pid uint32) (start int64, exe string, ok bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, "", false
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0, "", false
	}
	return int64(created.HighDateTime)<<32 | int64(created.LowDateTime), ProcessName(pid), Alive(pid)
}
