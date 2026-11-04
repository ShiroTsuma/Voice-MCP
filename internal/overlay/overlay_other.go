//go:build !windows

package overlay

import "time"

// Overlay shows nothing outside Windows.
type Overlay struct{}

func Start() (*Overlay, error)                      { return nil, nil }
func (o *Overlay) Set(change func(*State))          {}
func (o *Overlay) Flash(kind Kind, d time.Duration) {}
func (o *Overlay) SetTheme(name string)             {}
func (o *Overlay) SetScale(scale float64)           {}
