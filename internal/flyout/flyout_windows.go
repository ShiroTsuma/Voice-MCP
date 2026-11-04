// Package flyout shows a page in a borderless, rounded window anchored to the
// taskbar corner, like the flyouts of tray apps. The page renders in WebView2,
// which every Windows 11 has; github.com/jchv/go-webview2 loads it without
// cgo. The window is created on first use and then only hidden, so later
// opens are instant. It hides when it loses focus or when the page posts
// "close".
package flyout

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/jchv/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostMessage      = user32.NewProc("PostMessageW")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procGetForeground    = user32.NewProc("GetForegroundWindow")
	procGetWindowThread  = user32.NewProc("GetWindowThreadProcessId")
	procAttachInput      = user32.NewProc("AttachThreadInput")
	procBringToTop       = user32.NewProc("BringWindowToTop")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfo   = user32.NewProc("GetMonitorInfoW")
	procLoadCursor       = user32.NewProc("LoadCursorW")
	procSetThreadDpiCtx  = user32.NewProc("SetThreadDpiAwarenessContext")
	shcore               = windows.NewLazySystemDLL("shcore.dll")
	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
	gdi32                = windows.NewLazySystemDLL("gdi32.dll")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	dwmapi               = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmSetAttribute  = dwmapi.NewProc("DwmSetWindowAttribute")
	procMoveMemory       = windows.NewLazySystemDLL("kernel32.dll").NewProc("RtlMoveMemory")
	procLoadImage        = user32.NewProc("LoadImageW")
	procSendMessage      = user32.NewProc("SendMessageW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	wsPopup         = 0x80000000
	wsClipChildren  = 0x02000000
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008

	wmDestroy     = 0x0002
	wmMove        = 0x0003
	wmSize        = 0x0005
	wmActivate    = 0x0006
	wmClose       = 0x0010
	wmDpiChanged  = 0x02E0
	wmApp         = 0x8000
	wmRun         = wmApp + 1
	waInactive    = 0
	swHide        = 0
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040
	swpNoZOrder   = 0x0004
	swRestore     = 9

	wsOverlappedWindow = 0x00CF0000
	wsExAppWindow      = 0x00040000
	hwndTopmost        = ^uintptr(0) // (HWND)-1

	// Per-monitor v2: the window gets real pixels and WM_DPICHANGED, so the
	// page stays sharp at any scale.
	dpiPerMonitorV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4

	dwmaDarkMode     = 20
	dwmaCorners      = 33
	dwmaBorderColor  = 34
	dwmCornersRound  = 2
	monitorNearest   = 2
	mdtEffectiveDpi  = 0
	toggleQuietAfter = 400 * time.Millisecond
)

// Options describe the window. Sizes are in logical pixels (96 DPI).
type Options struct {
	Width, Height int
	// Margin keeps the window off the screen edges and the taskbar.
	Margin int
	// URL is asked on every show; the page reloads when it changed (for
	// example after a restart moved the server to another port).
	URL func() string
	// DataDir holds the WebView2 profile.
	DataDir string
	// Background paints the window before the page loads, as 0xRRGGBB.
	Background uint32
	// OnMessage gets what the page posts with chrome.webview.postMessage,
	// except "close", which hides the window.
	OnMessage func(msg string)
	// OnShow runs as JavaScript in the page each time the window appears.
	OnShow string
	// OnHide runs (on its own goroutine) each time the window goes away.
	OnHide func()
	Logf   func(format string, args ...any)
	// Normal makes an ordinary application window instead of a flyout: a title
	// bar, resizable, centred on first show, not on top, and it stays open when
	// it loses focus. Title is its caption.
	Normal bool
	Title  string
	// Passive makes a window that only shows something: it never takes the
	// focus or the mouse, so it does not disturb what the user works in.
	// ShowPassive brings it up.
	Passive bool
}

type Window struct {
	opt Options

	start   sync.Once
	ready   chan struct{}
	initErr error

	hwnd     uintptr
	chromium *edge.Chromium
	url      string // what the page shows; UI thread only

	mu    sync.Mutex
	queue []func()

	lastHide atomic.Int64 // unix nanos of the last hide on focus loss
	placed   bool         // a normal window was positioned once; the user moves it after
}

func New(opt Options) *Window {
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	return &Window{opt: opt, ready: make(chan struct{})}
}

// Available reports whether the WebView2 runtime is installed.
func Available() bool {
	v, err := webviewloader.GetInstalledVersion()
	return err == nil && v != ""
}

// Toggle shows the window, or hides it when it is open. A click on the tray
// icon first takes focus from an open window, which hides it; Toggle right
// after that leaves it hidden, so the same click closes the flyout.
func (w *Window) Toggle() error {
	if time.Since(time.Unix(0, w.lastHide.Load())) < toggleQuietAfter {
		return nil
	}
	return w.do(func() {
		if w.visible() {
			w.hide()
			return
		}
		w.show()
	})
}

// Show brings the window up and gives it focus.
func (w *Window) Show() error { return w.do(w.show) }

// ShowPassive brings the window up without activating it, for a Passive
// window: the focus stays where the user works.
func (w *Window) ShowPassive() error {
	return w.do(func() {
		if u := w.opt.URL(); u != w.url {
			w.url = u
			w.chromium.Navigate(u)
		} else if w.opt.OnShow != "" {
			w.chromium.Eval(w.opt.OnShow)
		}
		w.place(swpShowWindow | swpNoActivate)
		w.chromium.Show()
	})
}

// Visible reports whether the window is on screen.
func (w *Window) Visible() bool {
	select {
	case <-w.ready:
		return w.initErr == nil && w.visible()
	default:
		return false
	}
}

// Hide hides the window without destroying the page.
func (w *Window) Hide() {
	select {
	case <-w.ready:
		if w.initErr == nil {
			w.do(w.hide)
		}
	default:
	}
}

// do runs fn on the window's thread, creating the window first if needed.
func (w *Window) do(fn func()) error {
	w.start.Do(func() { go w.loop() })
	<-w.ready
	if w.initErr != nil {
		return w.initErr
	}
	w.mu.Lock()
	w.queue = append(w.queue, fn)
	w.mu.Unlock()
	procPostMessage.Call(w.hwnd, wmRun, 0, 0)
	return nil
}

// loop owns the window: Windows delivers a window's messages only to the
// thread that created it, and WebView2 needs a single-threaded apartment.
func (w *Window) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := w.create(); err != nil {
		w.initErr = err
		close(w.ready)
		return
	}
	close(w.ready)
	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

var (
	classOnce sync.Once
	classErr  error
	wndProcCb = windows.NewCallback(wndProc)
	windowsMu sync.Mutex
	byHandle  = map[uintptr]*Window{}
)

func (w *Window) create() error {
	if !Available() {
		return errors.New("the WebView2 runtime is not installed")
	}
	// A fresh thread: this only fails if COM is already set up here.
	windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	procSetThreadDpiCtx.Call(dpiPerMonitorV2)
	className, _ := windows.UTF16PtrFromString("voiceFlyout")
	classOnce.Do(func() {
		var inst windows.Handle
		windows.GetModuleHandleEx(0, nil, &inst)
		cursor, _, _ := procLoadCursor.Call(0, 32512) // IDC_ARROW
		brush, _, _ := procCreateSolidBrush.Call(uintptr(colorref(w.opt.Background)))
		wc := wndClassEx{
			WndProc: wndProcCb, Instance: uintptr(inst), Cursor: cursor,
			Background: brush, ClassName: className,
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("RegisterClassEx: %w", err)
		}
	})
	if classErr != nil {
		return classErr
	}
	title, _ := windows.UTF16PtrFromString(dirOr(w.opt.Title, "voice"))
	exStyle, style := uintptr(wsExToolWindow|wsExTopmost), uintptr(wsPopup|wsClipChildren)
	if w.opt.Normal {
		exStyle, style = wsExAppWindow, wsOverlappedWindow|wsClipChildren
	}
	if w.opt.Passive {
		exStyle |= wsExNoActivate | wsExTransparent
	}
	hwnd, _, err := procCreateWindowEx.Call(exStyle,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		style, 0, 0, uintptr(w.opt.Width), uintptr(w.opt.Height), 0, 0, 0, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowEx: %w", err)
	}
	w.hwnd = hwnd
	if w.opt.Normal {
		setIcon(hwnd)
	}
	windowsMu.Lock()
	byHandle[hwnd] = w
	windowsMu.Unlock()

	// Windows 11 draws the rounded corners, the shadow and a thin border; a
	// normal window gets a dark title bar.
	dwmSet(hwnd, dwmaDarkMode, 1)
	if !w.opt.Normal {
		dwmSet(hwnd, dwmaCorners, dwmCornersRound)
		dwmSet(hwnd, dwmaBorderColor, colorref(0x3a3c4a))
	}

	c := edge.NewChromium()
	c.DataPath = w.opt.DataDir
	c.MessageCallback = func(m string) {
		if m == "close" {
			w.hide()
			return
		}
		if w.opt.OnMessage != nil {
			go w.opt.OnMessage(m)
		}
	}
	if !c.Embed(hwnd) {
		return errors.New("WebView2 did not start")
	}
	w.chromium = c
	if s, err := c.GetSettings(); err == nil {
		s.PutAreDefaultContextMenusEnabled(false)
		s.PutAreDevToolsEnabled(false)
		s.PutIsZoomControlEnabled(false)
		s.PutIsStatusBarEnabled(false)
		s.PutAreBrowserAcceleratorKeysEnabled(false)
		s.PutIsPinchZoomEnabled(false)
		s.PutIsSwipeNavigationEnabled(false)
	}
	if c2 := c.GetController().GetICoreWebView2Controller2(); c2 != nil {
		bg := w.opt.Background
		c2.PutDefaultBackgroundColor(edge.COREWEBVIEW2_COLOR{A: 255, R: uint8(bg >> 16), G: uint8(bg >> 8), B: uint8(bg)})
	}
	c.Resize()
	return nil
}

func (w *Window) visible() bool {
	r, _, _ := procIsWindowVisible.Call(w.hwnd)
	return r != 0
}

// show places the window in the corner next to the taskbar of the screen
// with the mouse (that is where the tray icon was clicked) and focuses it.
func (w *Window) show() {
	if u := w.opt.URL(); u != w.url {
		w.url = u
		w.chromium.Navigate(u)
	} else if w.opt.OnShow != "" {
		w.chromium.Eval(w.opt.OnShow)
	}
	w.place(swpShowWindow)
	w.chromium.Show()
	w.focus()
	w.chromium.Focus()
}

// focus makes the window the active one, so that it hides again when the
// user clicks elsewhere. A tray click lets this process take the foreground;
// when Windows refuses anyway (the click came through a menu or another
// route), joining the input of the foreground thread for a moment does it.
// It only runs when the user opened the window.
func (w *Window) focus() {
	procSetForeground.Call(w.hwnd)
	fg, _, _ := procGetForeground.Call()
	if fg == w.hwnd || fg == 0 {
		return
	}
	other, _, _ := procGetWindowThread.Call(fg, 0)
	me := windows.GetCurrentThreadId()
	if r, _, _ := procAttachInput.Call(uintptr(me), other, 1); r == 0 {
		return
	}
	procSetForeground.Call(w.hwnd)
	procBringToTop.Call(w.hwnd)
	procAttachInput.Call(uintptr(me), other, 0)
}

func (w *Window) place(flags uintptr) {
	if w.opt.Normal {
		w.centre(flags)
		return
	}
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon, _, _ := procMonitorFromPoint.Call(uintptr(*(*uint64)(unsafe.Pointer(&pt))), monitorNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	var dpiX, dpiY uint32 = 96, 96
	procGetDpiForMonitor.Call(mon, mdtEffectiveDpi, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY)))
	scale := func(v int) int32 { return int32(v * int(dpiX) / 96) }
	work, full := mi.Work, mi.Monitor
	margin := scale(w.opt.Margin)
	width, height := scale(w.opt.Width), scale(w.opt.Height)
	if maxH := work.Bottom - work.Top - 2*margin; height > maxH {
		height = maxH
	}
	// Bottom right unless the taskbar sits at the top or on the left.
	x := work.Right - width - margin
	y := work.Bottom - height - margin
	if work.Left > full.Left {
		x = work.Left + margin
	}
	if work.Top > full.Top {
		y = work.Top + margin
	}
	procSetWindowPos.Call(w.hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(width), uintptr(height), flags)
}

// centre puts a normal window in the middle of the screen with the mouse the
// first time; later it stays where the user left it, only shown and raised.
func (w *Window) centre(flags uintptr) {
	if w.placed {
		procShowWindow.Call(w.hwnd, swRestore)
		return
	}
	w.placed = true
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon, _, _ := procMonitorFromPoint.Call(uintptr(*(*uint64)(unsafe.Pointer(&pt))), monitorNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	var dpiX, dpiY uint32 = 96, 96
	procGetDpiForMonitor.Call(mon, mdtEffectiveDpi, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY)))
	width, height := int32(w.opt.Width*int(dpiX)/96), int32(w.opt.Height*int(dpiX)/96)
	work := mi.Work
	width, height = min(width, work.Right-work.Left), min(height, work.Bottom-work.Top)
	x := work.Left + (work.Right-work.Left-width)/2
	y := work.Top + (work.Bottom-work.Top-height)/2
	procSetWindowPos.Call(w.hwnd, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), flags|swpNoZOrder)
}

func (w *Window) hide() {
	was := w.visible()
	if w.chromium != nil {
		w.chromium.Hide()
	}
	procShowWindow.Call(w.hwnd, swHide)
	if was && w.opt.OnHide != nil {
		go w.opt.OnHide()
	}
}

func (w *Window) run() {
	w.mu.Lock()
	q := w.queue
	w.queue = nil
	w.mu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	windowsMu.Lock()
	w := byHandle[hwnd]
	windowsMu.Unlock()
	if w != nil {
		switch msg {
		case wmRun:
			w.run()
			return 0
		case wmActivate:
			if wp&0xffff == waInactive && w.visible() && !w.opt.Normal && !w.opt.Passive {
				w.lastHide.Store(time.Now().UnixNano())
				w.hide()
			}
		case wmSize:
			if w.chromium != nil {
				w.chromium.Resize()
			}
		case wmMove:
			if w.chromium != nil {
				w.chromium.NotifyParentWindowPositionChanged()
			}
		case wmDpiChanged:
			if w.opt.Normal {
				// Windows suggests the size for the new scale (lParam).
				var r rect
				procMoveMemory.Call(uintptr(unsafe.Pointer(&r)), lp, unsafe.Sizeof(r))
				procSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpNoZOrder|swpNoActivate)
				return 0
			}
			// Moved to a screen with another scale: size it for that screen.
			w.place(swpNoActivate)
			return 0
		case wmClose:
			w.hide()
			return 0
		case wmDestroy:
			windowsMu.Lock()
			delete(byHandle, hwnd)
			windowsMu.Unlock()
		}
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wp, lp)
	return r
}

func dwmSet(hwnd uintptr, attr uint32, v uint32) {
	procDwmSetAttribute.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&v)), 4)
}

// colorref turns 0xRRGGBB into a Windows COLORREF (0x00BBGGRR).
func colorref(rgb uint32) uint32 {
	return (rgb&0xff)<<16 | rgb&0xff00 | (rgb>>16)&0xff
}

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

func dirOr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// setIcon gives a window voice's icon (resource 1 in voice.exe): the large one
// for Alt+Tab, the small one for the title bar and the taskbar.
func setIcon(hwnd uintptr) {
	const imageIcon, lrDefaultColor, wmSetIcon, iconSmall, iconBig = 1, 0, 0x0080, 0, 1
	const smCxIcon, smCxSmIcon = 11, 49
	var inst windows.Handle
	windows.GetModuleHandleEx(0, nil, &inst)
	for _, kind := range []struct{ which, metric uintptr }{{iconBig, smCxIcon}, {iconSmall, smCxSmIcon}} {
		size, _, _ := procGetSystemMetrics.Call(kind.metric)
		icon, _, _ := procLoadImage.Call(uintptr(inst), 1, imageIcon, size, size, lrDefaultColor)
		if icon != 0 {
			procSendMessage.Call(hwnd, wmSetIcon, kind.which, icon)
		}
	}
}
