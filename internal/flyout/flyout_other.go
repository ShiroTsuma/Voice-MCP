//go:build !windows

package flyout

import "errors"

type Options struct {
	Width, Height int
	Margin        int
	URL           func() string
	DataDir       string
	Background    uint32
	OnMessage     func(msg string)
	OnShow        string
	OnHide        func()
	Logf          func(format string, args ...any)
	Normal        bool
	Title         string
	Passive       bool
	Centre        bool
}

type Window struct{}

func New(Options) *Window { return &Window{} }

func Available() bool { return false }

var errUnsupported = errors.New("the settings flyout needs Windows")

func (w *Window) Toggle() error              { return errUnsupported }
func (w *Window) Show() error                { return errUnsupported }
func (w *Window) Hide()                      {}
func (w *Window) ShowPassive() error         { return errUnsupported }
func (w *Window) Visible() bool              { return false }
func (w *Window) SetClickThrough(bool) error { return errUnsupported }
func (w *Window) Drag() error                { return errUnsupported }
