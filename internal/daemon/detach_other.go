//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
	"time"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func Detach(cmd *exec.Cmd) { detach(cmd) }

func StartDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func SingleInstance(string) (func(), error) { return func() {}, nil }

func WaitExit(int, time.Duration) {}
