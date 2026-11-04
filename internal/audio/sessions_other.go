//go:build !windows

package audio

type Sessions struct{}

func NewSessions() (*Sessions, error)     { return nil, errUnsupported }
func (*Sessions) Duck(float64)            {}
func (*Sessions) Restore(float64)         {}
func (*Sessions) NameSelf(string, string) {}

type SessionInfo struct {
	PID    uint32
	Active bool
	Volume float32
	Name   string
}

func (*Sessions) List() []SessionInfo { return nil }
