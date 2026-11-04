// Package inject delivers dictated text to an agent: into the console input of
// a terminal agent's process (whatever window is in front), or as typed keys
// into the foreground window for desktop apps.
package inject

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procWriteConsoleInput     = kernel32.NewProc("WriteConsoleInputW")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")

	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")

	user32                       = windows.NewLazySystemDLL("user32.dll")
	procSendInput                = user32.NewProc("SendInput")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
)

const (
	keyEvent  = 0x0001
	vkReturn  = 0x0D
	inputKbd  = 1
	kfUnicode = 0x0004
	kfKeyUp   = 0x0002
)

// keyEventRecord is INPUT_RECORD with a KEY_EVENT_RECORD.
type keyEventRecord struct {
	EventType       uint16
	_               uint16
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

// A process has at most one console, so attaching is serialized.
var consoleMu sync.Mutex

// ToConsole writes text as key presses into the console input of process pid,
// as if typed there; submit adds Enter. It works for agents in conhost and
// Windows Terminal, wherever the focus is. The calling process must have no
// console of its own (the detached voice background process has none).
func ToConsole(pid uint32, text string, submit bool) error {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("attach to the console of process %d: %w", pid, err)
	}
	defer procFreeConsole.Call()
	// While attached, Ctrl+C in that console would reach this process too.
	procSetConsoleCtrlHandler.Call(0, 1)
	defer procSetConsoleCtrlHandler.Call(0, 0)
	name, _ := windows.UTF16PtrFromString("CONIN$")
	in, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("open the console input of process %d: %w", pid, err)
	}
	defer windows.CloseHandle(in)
	var records []keyEventRecord
	for _, c := range windows.StringToUTF16(text) {
		if c == 0 {
			continue
		}
		down := keyEventRecord{EventType: keyEvent, KeyDown: 1, RepeatCount: 1, UnicodeChar: c}
		up := down
		up.KeyDown = 0
		records = append(records, down, up)
	}
	if err := writeConsole(in, records); err != nil {
		return err
	}
	if submit {
		// Agents that tell typing from pasting by speed (Codex) would take an
		// Enter right after the text as a new line, not as sending it.
		time.Sleep(300 * time.Millisecond)
		enter := keyEventRecord{EventType: keyEvent, KeyDown: 1, RepeatCount: 1, VirtualKeyCode: vkReturn, UnicodeChar: '\r'}
		release := enter
		release.KeyDown = 0
		return writeConsole(in, []keyEventRecord{enter, release})
	}
	return nil
}

func writeConsole(in windows.Handle, records []keyEventRecord) error {
	for len(records) > 0 {
		var written uint32
		r, _, err := procWriteConsoleInput.Call(uintptr(in), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
		if r == 0 {
			return fmt.Errorf("write console input: %w", err)
		}
		if written == 0 {
			return errors.New("console input accepted nothing")
		}
		records = records[written:]
	}
	return nil
}

// keyboardInput is INPUT with a KEYBDINPUT (64-bit layout).
type keyboardInput struct {
	Type      uint32
	_         uint32
	VK        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
	_         [8]byte
}

// Foreground returns the foreground window and the process that owns it.
func Foreground() (hwnd uintptr, pid uint32) {
	hwnd, _, _ = procGetForegroundWindow.Call()
	if hwnd != 0 {
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	}
	return hwnd, pid
}

// ToWindow types text into window hwnd as Unicode key presses, provided it is
// still the foreground window; submit adds Enter.
func ToWindow(hwnd uintptr, text string, submit bool) error {
	waitModifiersUp()
	if now, _ := Foreground(); now != hwnd {
		return ErrFocusMoved
	}
	var inputs []keyboardInput
	for _, c := range windows.StringToUTF16(text) {
		if c == 0 {
			continue
		}
		inputs = append(inputs,
			keyboardInput{Type: inputKbd, Scan: c, Flags: kfUnicode},
			keyboardInput{Type: inputKbd, Scan: c, Flags: kfUnicode | kfKeyUp})
	}
	if err := sendInput(inputs); err != nil {
		return err
	}
	if submit {
		time.Sleep(300 * time.Millisecond)
		if now, _ := Foreground(); now != hwnd {
			return ErrFocusMoved
		}
		return sendInput([]keyboardInput{{Type: inputKbd, VK: vkReturn}, {Type: inputKbd, VK: vkReturn, Flags: kfKeyUp}})
	}
	return nil
}

// ErrFocusMoved means another window came to the front before the text was typed.
var ErrFocusMoved = errors.New("the focus moved to another window")

// ToClipboard puts text on the clipboard, for when it could not be typed.
func ToClipboard(text string) error {
	const cfUnicodeText, gmemMoveable = 13, 0x0002
	utf16 := windows.StringToUTF16(text)
	size := uintptr(len(utf16) * 2)
	mem, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if mem == 0 {
		return fmt.Errorf("clipboard memory: %w", err)
	}
	p, _, err := procGlobalLock.Call(mem)
	if p == 0 {
		procGlobalFree.Call(mem)
		return fmt.Errorf("clipboard memory: %w", err)
	}
	procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&utf16[0])), size)
	procGlobalUnlock.Call(mem)
	// Another program may hold the clipboard for a moment.
	for i := 0; ; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			break
		}
		if i == 10 {
			procGlobalFree.Call(mem)
			return errors.New("the clipboard is in use")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, mem); r == 0 {
		procGlobalFree.Call(mem)
		return fmt.Errorf("set clipboard: %w", err)
	}
	return nil // the clipboard owns mem now
}

// ProcessName is the executable file name of process pid, e.g. "WindowsTerminal.exe".
func ProcessName(pid uint32) string {
	path := ProcessPath(pid)
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// Alive reports whether process pid still runs.
func Alive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	r, _ := windows.WaitForSingleObject(h, 0)
	return r == uint32(windows.WAIT_TIMEOUT)
}

func sendInput(inputs []keyboardInput) error {
	if len(inputs) == 0 {
		return nil
	}
	n, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if int(n) != len(inputs) {
		return fmt.Errorf("send input (blocked by a higher-privilege window?): %w", err)
	}
	return nil
}

// ProcessPath is pid's executable, a full path; "" when it cannot be read.
func ProcessPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
