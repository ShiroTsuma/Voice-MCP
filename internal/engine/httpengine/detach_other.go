//go:build !windows

package httpengine

import (
	"os"
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// stop ends a server, with the processes in its session.
func stop(p *os.Process) {
	if p == nil {
		return
	}
	if syscall.Kill(-p.Pid, syscall.SIGKILL) != nil {
		p.Kill()
	}
}
