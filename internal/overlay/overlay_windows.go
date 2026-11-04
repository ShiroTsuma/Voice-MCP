package overlay

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"voice/internal/overlay/look"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	gdi32                   = windows.NewLazySystemDLL("gdi32.dll")
	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procGetMessage          = user32.NewProc("GetMessageW")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostMessage         = user32.NewProc("PostMessageW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	procCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procDeleteDC            = gdi32.NewProc("DeleteDC")
	procRtlMoveMemory       = windows.NewLazySystemDLL("kernel32.dll").NewProc("RtlMoveMemory")
)

const (
	wsPopup         = 0x80000000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExNoActivate  = 0x08000000
	wmTimer         = 0x0113
	wmNCHitTest     = 0x0084
	wmApp           = 0x8000
	htTransparent   = ^uintptr(0) // -1
	swHide          = 0
	swShowNA        = 8
	ulwAlpha        = 2
	margin          = 8 // pixels between the canvas and the corner of the work area
	frame           = 33 * time.Millisecond
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

// Overlay is the circle's window, on its own thread.
type Overlay struct {
	mu        sync.Mutex
	state     State
	hwnd      uintptr
	err       error // why the window could not be made
	start     time.Time
	themeName string  // what Set asked for; the window thread switches to it
	scale     float64 // size on screen relative to the theme's canvas; 0 is 1
	// Used on the window's thread only.
	scene look.Scene
	inner look.Scene // the agent's ring inside the user's: the same theme, smaller
	shown string     // the theme scene draws
	last  time.Time
	timer bool
	pos   point // where it shows this time
	side  int   // its size on screen this time
}

// Start creates the (hidden) window. Without one it returns nil and why;
// Set on a nil Overlay does nothing.
func Start() (*Overlay, error) {
	o := &Overlay{start: time.Now()}
	ready := make(chan bool)
	go o.loop(ready)
	if !<-ready {
		return nil, fmt.Errorf("dictation overlay: %w", o.err)
	}
	return o, nil
}

// Set changes the state and redraws.
func (o *Overlay) Set(change func(*State)) {
	if o == nil {
		return
	}
	o.mu.Lock()
	change(&o.state)
	o.mu.Unlock()
	procPostMessage.Call(o.hwnd, wmApp, 0, 0)
}

// SetScale sets the size on screen, e.g. 1.5 for 150%.
func (o *Overlay) SetScale(f float64) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.scale = max(0.5, min(f, 3))
	o.mu.Unlock()
	procPostMessage.Call(o.hwnd, wmApp, 0, 0)
}

// SetTheme picks the theme by name (overlay.Themes); an unknown name gives the default.
func (o *Overlay) SetTheme(name string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.themeName = name
	o.mu.Unlock()
	procPostMessage.Call(o.hwnd, wmApp, 0, 0)
}

// Flash shows kind (Done or Problem) for d, then fades it out.
func (o *Overlay) Flash(kind Kind, d time.Duration) {
	o.Set(func(s *State) { s.Flash, s.FlashUntil = kind, time.Now().Add(d) })
}

func (o *Overlay) loop(ready chan<- bool) {
	runtime.LockOSThread() // the window belongs to this thread
	name, _ := windows.UTF16PtrFromString("voice-dictation-overlay")
	wndProc := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		switch msg {
		case wmNCHitTest:
			return htTransparent // clicks go to whatever is below
		case wmTimer, wmApp:
			o.paint(hwnd)
			return 0
		}
		r, _, _ := procDefWindowProc.Call(hwnd, msg, wp, lp)
		return r
	})
	type wndClassEx struct {
		Size, Style                   uint32
		WndProc                       uintptr
		ClsExtra, WndExtra            int32
		Instance, Icon, Cursor, Bkgnd uintptr
		MenuName, ClassName           *uint16
		IconSm                        uintptr
	}
	var inst windows.Handle
	windows.GetModuleHandleEx(0, nil, &inst)
	wc := wndClassEx{WndProc: wndProc, ClassName: name, Instance: uintptr(inst)}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		o.err = err
		ready <- false
		return
	}
	hwnd, _, err := procCreateWindowEx.Call(wsExTopmost|wsExToolWindow|wsExLayered|wsExTransparent|wsExNoActivate,
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), wsPopup, 0, 0, 1, 1, 0, 0, uintptr(inst), 0)
	if hwnd == 0 {
		o.err = err
		ready <- false
		return
	}
	o.hwnd = hwnd
	ready <- true
	var msg struct {
		Hwnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      point
		_       uint32
	}
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// paint moves the blobs a frame toward the current state and draws them. A
// frame timer runs while anything is visible: the blobs always move a little.
func (o *Overlay) paint(hwnd uintptr) {
	o.mu.Lock()
	s, name, scale := o.state, o.themeName, o.scale
	o.mu.Unlock()
	if o.scene == nil || name != o.shown {
		// A new theme starts from nothing and grows in like any appearance.
		o.scene, o.inner, o.shown = theme(name).New(), theme(name).New(), name
	}
	now := time.Now()
	dt := now.Sub(o.last).Seconds()
	if o.last.IsZero() || dt > 0.2 {
		dt = 1.0 / 30
	}
	o.last = now
	// Two rings of one theme: the user's (dictation) and, inside it, the
	// agent's while it speaks: the recording animation moving with the
	// voice, smaller and in the speaking character's colour.
	t := now.Sub(o.start).Seconds()
	outer := o.scene.Step(s.Show(now), s, t, dt)
	si, innerKind := s, Hidden
	if s.Speaking {
		innerKind, si.Level = Recording, s.SpeakLevel
	}
	inner := o.inner.Step(innerKind, si, t, dt)
	if !outer && !inner {
		procKillTimer.Call(hwnd, 1)
		procShowWindow.Call(hwnd, swHide)
		o.timer = false
		return
	}
	if !o.timer {
		// Placed once per appearance: it stays put while the focus moves.
		o.pos = corner(int32(onScreen(o.scene.Size(), scale)))
		procSetTimer.Call(hwnd, 1, uintptr(frame/time.Millisecond), 0)
		o.timer = true
		if Logf != nil {
			Logf("overlay: showing %v at %d,%d", s.Show(now), o.pos.X, o.pos.Y)
		}
	}
	side := onScreen(o.scene.Size(), scale)
	if side != o.side {
		o.pos, o.side = corner(int32(side)), side // a new size keeps to the corner
	}
	size := o.scene.Size()
	var px []byte
	if outer {
		px = o.scene.Render()
	} else {
		px = make([]byte, size*size*4)
	}
	if inner {
		tint := s.Tint
		if tint == 0 {
			tint = defaultTint
		}
		px = within(px, tinted(o.inner.Render(), tint), size, innerShare)
	}
	o.update(hwnd, scaled(px, size, side), int32(side), o.pos)
	procShowWindow.Call(hwnd, swShowNA)
}

// corner is the circle's top left in the bottom right corner of the work area
// (above the taskbar) of the monitor the user works on: the one with the
// window in front, else the primary monitor.
func corner(size int32) point {
	const monitorDefaultToPrimary = 1
	fg, _, _ := procGetForegroundWindow.Call()
	mon, _, _ := procMonitorFromWindow.Call(fg, monitorDefaultToPrimary)
	var info struct {
		Size    uint32
		Monitor rect
		Work    rect
		Flags   uint32
	}
	info.Size = uint32(unsafe.Sizeof(info))
	procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&info)))
	return point{info.Work.Right - size - margin, info.Work.Bottom - size - margin}
}

// update hands the premultiplied pixels to the layered window.
func (o *Overlay) update(hwnd uintptr, px []byte, side int32, pos point) {
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDC.Call(mem)
	type bitmapInfoHeader struct {
		Size                         uint32
		Width, Height                int32
		Planes, BitCount             uint16
		Compression, SizeImage       uint32
		XPelsPerMeter, YPelsPerMeter int32
		ClrUsed, ClrImportant        uint32
	}
	bi := bitmapInfoHeader{Width: side, Height: -side, Planes: 1, BitCount: 32} // top row first
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits uintptr
	bmp, _, _ := procCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		return
	}
	defer procDeleteObject.Call(bmp)
	procRtlMoveMemory.Call(bits, uintptr(unsafe.Pointer(&px[0])), uintptr(len(px)))
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)
	size := point{side, side}
	src := point{}
	blend := uint32(0x01<<24 | 0xFF<<16) // AC_SRC_OVER, no flags, constant alpha 255, AC_SRC_ALPHA
	procUpdateLayeredWindow.Call(hwnd, screen, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&size)),
		mem, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
}

// onScreen is a canvas of size pixels at scale (0 counts as 1).
func onScreen(size int, scale float64) int {
	if scale <= 0 {
		return size
	}
	return int(float64(size)*scale + 0.5)
}
