//go:build windows

package audio

import (
	"os/exec"
	"syscall"
)

// hideConsole keeps a console program (ffmpeg) from opening a window when the
// background process, which has none, starts it.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
