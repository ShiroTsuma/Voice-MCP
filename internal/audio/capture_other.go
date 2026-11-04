//go:build !windows

package audio

import (
	"context"
	"time"
)

type Capture struct{}

func InputDevices() ([]string, error)                          { return nil, errUnsupported }
func OpenCapture(string, int, time.Duration) (*Capture, error) { return nil, errUnsupported }
func (*Capture) Read(context.Context) ([]float32, error)       { return nil, errUnsupported }
func (*Capture) Close()                                        {}
