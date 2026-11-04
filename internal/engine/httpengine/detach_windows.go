package httpengine

import (
	"os/exec"
	"syscall"
)

// detach keeps the server running independently of the voice process that
// started it, without a console window.
func detach(cmd *exec.Cmd) {
	const detachedProcess, newGroup, noWindow = 0x00000008, 0x00000200, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newGroup | noWindow, HideWindow: true}
}
