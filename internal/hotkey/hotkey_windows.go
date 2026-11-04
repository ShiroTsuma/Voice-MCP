// Package hotkey registers system-wide keyboard shortcuts.
package hotkey

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procRegister     = user32.NewProc("RegisterHotKey")
	procUnregister   = user32.NewProc("UnregisterHotKey")
	procGetMessage   = user32.NewProc("GetMessageW")
	procPostThreadMs = user32.NewProc("PostThreadMessageW")
	procPeekMessage  = user32.NewProc("PeekMessageW")
)

const (
	modAlt      = 0x1
	modControl  = 0x2
	modShift    = 0x4
	modWin      = 0x8
	modNoRepeat = 0x4000
	wmHotkey    = 0x0312
	wmApp       = 0x8000
)

var keys = map[string]uint32{
	"SPACE": 0x20, "ENTER": 0x0D, "ESC": 0x1B, "ESCAPE": 0x1B, "TAB": 0x09, "BACKSPACE": 0x08,
	"PAUSE": 0x13, "INSERT": 0x2D, "DELETE": 0x2E, "HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PAGEDOWN": 0x22,
	"LEFT": 0x25, "UP": 0x26, "RIGHT": 0x27, "DOWN": 0x28, "SCROLLLOCK": 0x91,
	"MEDIAPLAYPAUSE": 0xB3, "MEDIASTOP": 0xB2, "VOLUMEMUTE": 0xAD,
	"`": 0xC0, "-": 0xBD, "=": 0xBB, "[": 0xDB, "]": 0xDD, ";": 0xBA, "'": 0xDE, ",": 0xBC, ".": 0xBE, "/": 0xBF, "\\": 0xDC,
}

// Parse reads "Ctrl+Alt+Space" style combinations into modifiers and a
// virtual key code.
func Parse(spec string) (mods, vk uint32, err error) {
	parts := strings.Split(strings.ReplaceAll(spec, " ", ""), "+")
	for i, p := range parts {
		up := strings.ToUpper(p)
		last := i == len(parts)-1
		switch {
		case up == "CTRL" || up == "CONTROL":
			mods |= modControl
		case up == "ALT":
			mods |= modAlt
		case up == "SHIFT":
			mods |= modShift
		case up == "WIN" || up == "SUPER":
			mods |= modWin
		case !last:
			return 0, 0, fmt.Errorf("hotkey %q: %q is not a modifier", spec, p)
		case len(up) == 1 && up[0] >= 'A' && up[0] <= 'Z', len(up) == 1 && up[0] >= '0' && up[0] <= '9':
			vk = uint32(up[0])
		case len(up) >= 2 && up[0] == 'F' && up[1] >= '1' && up[1] <= '9':
			var n int
			if _, err := fmt.Sscanf(up[1:], "%d", &n); err != nil || n < 1 || n > 24 {
				return 0, 0, fmt.Errorf("hotkey %q: unknown key %q", spec, p)
			}
			vk = uint32(0x70 + n - 1)
		default:
			code, ok := keys[up]
			if !ok {
				return 0, 0, fmt.Errorf("hotkey %q: unknown key %q", spec, p)
			}
			vk = code
		}
	}
	if vk == 0 {
		return 0, 0, fmt.Errorf("hotkey %q: no key", spec)
	}
	if mods == 0 {
		return 0, 0, fmt.Errorf("hotkey %q: needs at least one of Ctrl, Alt, Shift, Win", spec)
	}
	return mods, vk, nil
}

// Set is a group of hotkeys on one message-loop thread. Hotkeys belong to
// the thread that registers them, so registration happens there too.
type Set struct {
	mu       sync.Mutex
	thread   uint32
	handlers map[int]func()
	jobs     chan func()
	started  bool
}

func (s *Set) start() {
	if s.started {
		return
	}
	s.started = true
	s.handlers = map[int]func(){}
	s.jobs = make(chan func(), 8)
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		s.thread = windows.GetCurrentThreadId()
		var msg struct {
			hwnd    uintptr
			message uint32
			wParam  uintptr
			lParam  uintptr
			time    uint32
			pt      [2]int32
			private uint32
		}
		// Create the thread's message queue before anyone posts to it.
		procPeekMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0)
		close(ready)
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			switch msg.message {
			case wmHotkey:
				s.mu.Lock()
				fn := s.handlers[int(msg.wParam)]
				s.mu.Unlock()
				if fn != nil {
					go fn()
				}
			case wmApp:
			drain:
				for {
					select {
					case job := <-s.jobs:
						job()
					default:
						break drain
					}
				}
			}
		}
	}()
	<-ready
}

// run executes fn on the message-loop thread and waits for it.
func (s *Set) run(fn func() error) error {
	done := make(chan error, 1)
	s.jobs <- func() { done <- fn() }
	procPostThreadMs.Call(uintptr(s.thread), wmApp, 0, 0)
	return <-done
}

// Register binds spec (e.g. "Ctrl+Alt+Space") to fn under id, replacing an
// earlier binding with the same id. An empty spec only removes it.
func (s *Set) Register(id int, spec string, fn func()) error {
	s.mu.Lock()
	s.start()
	s.mu.Unlock()
	var mods, vk uint32
	if spec != "" {
		var err error
		if mods, vk, err = Parse(spec); err != nil {
			return err
		}
	}
	return s.run(func() error {
		procUnregister.Call(0, uintptr(id))
		s.mu.Lock()
		delete(s.handlers, id)
		s.mu.Unlock()
		if spec == "" {
			return nil
		}
		if r, _, err := procRegister.Call(0, uintptr(id), uintptr(mods|modNoRepeat), uintptr(vk)); r == 0 {
			return fmt.Errorf("hotkey %s is taken by another program or invalid: %v", spec, err)
		}
		s.mu.Lock()
		s.handlers[id] = fn
		s.mu.Unlock()
		return nil
	})
}

// Escape is the virtual key code of Esc, for RegisterKey.
const Escape = 0x1B

// RegisterKey binds a key with no modifier (Esc) to fn under id, for a short
// while: it takes the key from every program until it is removed again with
// Register(id, "", nil). For a moment such as dictation, never for long.
func (s *Set) RegisterKey(id int, vk uint32, fn func()) error {
	s.mu.Lock()
	s.start()
	s.mu.Unlock()
	return s.run(func() error {
		procUnregister.Call(0, uintptr(id))
		if r, _, err := procRegister.Call(0, uintptr(id), uintptr(modNoRepeat), uintptr(vk)); r == 0 {
			return fmt.Errorf("key %#x is taken by another program: %v", vk, err)
		}
		s.mu.Lock()
		s.handlers[id] = fn
		s.mu.Unlock()
		return nil
	})
}
