package main

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detach lets the speech process outlive the hook and keeps it from
// receiving the client's console signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: true}
}

// hidden runs a short helper (the claude and codex CLIs, `serve stop`) with
// a console that has no window. Unlike detach it gives the helper a console,
// so the processes it starts in turn share it instead of each opening a
// window of their own.
func hidden(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup, HideWindow: true}
}
