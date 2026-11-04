//go:build !windows

package hotkey

import "errors"

type Set struct{}

func Parse(string) (uint32, uint32, error) { return 0, 0, errors.New("hotkeys need Windows") }

func (*Set) Register(int, string, func()) error { return errors.New("hotkeys need Windows") }

// Escape is the virtual key code of Esc.
const Escape = 0x1B

func (*Set) RegisterKey(int, uint32, func()) error { return errors.New("hotkeys need Windows") }
