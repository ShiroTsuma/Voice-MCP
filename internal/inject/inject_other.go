//go:build !windows

package inject

import "errors"

var errUnsupported = errors.New("dictation is only supported on Windows")

// ErrFocusMoved means another window came to the front before the text was typed.
var ErrFocusMoved = errors.New("the focus moved to another window")

func ToConsole(pid uint32, text string, submit bool) error  { return errUnsupported }
func Foreground() (uintptr, uint32)                         { return 0, 0 }
func ToWindow(hwnd uintptr, text string, submit bool) error { return errUnsupported }
func ToClipboard(text string) error                         { return errUnsupported }
func ProcessName(pid uint32) string                         { return "" }
func ProcessPath(pid uint32) string                         { return "" }
func Alive(pid uint32) bool                                 { return false }
func Ancestors(pid uint32) []uint32                         { return nil }
func HostApp(pid uint32, apps map[string]bool) (uint32, string) {
	return 0, ""
}
func MainWindow(pid uint32) uintptr { return 0 }
func Focus(hwnd uintptr) error      { return errUnsupported }

func Identity(pid uint32) (int64, string, bool) { return 0, "", false }
