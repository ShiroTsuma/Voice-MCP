package daemon

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Detach is detach for callers outside the package (restarts).
func Detach(cmd *exec.Cmd) { detach(cmd) }

const breakawayFromJob = 0x01000000

// detach starts the background process outside the caller's console and
// process group, so it outlives MCP servers and hooks. It also leaves the
// caller's job: shared-mcp runs each backend in a kill-on-close job, and the
// background process would otherwise die with the MCP server that started it.
func detach(cmd *exec.Cmd) {
	const detachedProcess, newGroup, noWindow = 0x00000008, 0x00000200, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newGroup | noWindow | breakawayFromJob, HideWindow: true}
}

// StartDetached starts exe detached and lets it go. A job that does not
// allow breakaway refuses the flag, so it then starts inside the job.
func StartDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	detach(cmd)
	err := cmd.Start()
	if err != nil {
		cmd = exec.Command(exe, args...)
		detach(cmd)
		cmd.SysProcAttr.CreationFlags &^= breakawayFromJob
		err = cmd.Start()
	}
	if err != nil {
		return err
	}
	return cmd.Process.Release()
}

// WaitExit waits up to timeout for process pid to end; restarts use it so
// the new process can take the single-instance lock.
func WaitExit(pid int, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}

// SingleInstance holds a named mutex for the life of the process; a second
// background process of the same installation gets ErrRunning.
func SingleInstance(id string) (release func(), err error) {
	name, _ := windows.UTF16PtrFromString(`Local\voice-serve-` + id)
	h, err := windows.CreateMutex(nil, true, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return nil, ErrRunning
	}
	if h == 0 {
		return nil, fmt.Errorf("single-instance lock: %w", err)
	}
	return func() { windows.CloseHandle(h) }, nil
}
