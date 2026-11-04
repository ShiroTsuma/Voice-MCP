// Package autostart starts the voice background process at login through a
// shortcut in the user's Startup folder. It is off unless the user turns it on.
package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

// Supported reports whether this OS has a login autostart.
const Supported = true

func shortcut() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Startup, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "voice.lnk"), nil
}

// Enabled reports whether the Startup shortcut exists.
func Enabled() bool {
	p, err := shortcut()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Set creates the shortcut, starting `exe serve`, or removes it.
func Set(on bool, exe string) error {
	p, err := shortcut()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return createShortcut(p, exe)
}

// createShortcut writes a .lnk at p that runs `exe serve`.
func createShortcut(p, exe string) error {
	// COM must stay on one thread from initialization to release.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		var oleErr *ole.OleError
		// S_FALSE: already initialized on this thread.
		if !errors.As(err, &oleErr) || oleErr.Code() != 1 {
			return err
		}
	}
	defer ole.CoUninitialize()
	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer unknown.Release()
	shell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()
	v, err := oleutil.CallMethod(shell, "CreateShortcut", p)
	if err != nil {
		return err
	}
	link := v.ToIDispatch()
	defer link.Release()
	for name, value := range map[string]string{
		"TargetPath":       exe,
		"Arguments":        "serve",
		"WorkingDirectory": filepath.Dir(exe),
		"Description":      "voice: speech for AI agents (tray icon, hotkeys)",
		"IconLocation":     exe + ",0",
	} {
		if _, err := oleutil.PutProperty(link, name, value); err != nil {
			return err
		}
	}
	_, err = oleutil.CallMethod(link, "Save")
	return err
}
