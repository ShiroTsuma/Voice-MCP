package httpengine

import (
	"os/exec"
	"syscall"
)

// detach keeps the server running independently of the voice process that
// started it, without a console window. It gets a console that has no window
// rather than none (DETACHED_PROCESS): a venv's python.exe is a launcher that
// starts the real interpreter as a child, and a child of a process without a
// console opens a console window of its own.
func detach(cmd *exec.Cmd) {
	const newGroup, noWindow = 0x00000200, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: newGroup | noWindow, HideWindow: true}
}
