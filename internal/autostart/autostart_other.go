//go:build !windows

package autostart

import "errors"

// Supported reports whether this OS has a login autostart.
const Supported = false

func Enabled() bool { return false }

func Set(on bool, exe string) error {
	if on {
		return errors.New("autostart is only available on Windows")
	}
	return nil
}
