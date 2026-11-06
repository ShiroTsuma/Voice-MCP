package daemon

import (
	"os/exec"
	"path/filepath"
)

// ownDir starts a background process in its executable's folder, not in the
// folder of whoever started it: it lives on for hours, and Windows keeps a
// process's working folder in use, so the agent's project folder that
// happened to start it could not be removed or renamed meanwhile. A folder
// the caller chose stays.
func ownDir(cmd *exec.Cmd) {
	if cmd.Dir != "" {
		return
	}
	exe := cmd.Path
	if !filepath.IsAbs(exe) {
		if p, err := exec.LookPath(exe); err == nil {
			exe = p
		}
	}
	if abs, err := filepath.Abs(exe); err == nil {
		cmd.Dir = filepath.Dir(abs)
	}
}
