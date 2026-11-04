//go:build !windows

package audio

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("audio output is implemented for Windows only")

type device struct{}

func Devices() ([]string, error)                      { return nil, errUnsupported }
func openDevice(string, int) (*device, error)         { return nil, errUnsupported }
func (*device) play(context.Context, []float32) error { return errUnsupported }
func (*device) close() error                          { return nil }
