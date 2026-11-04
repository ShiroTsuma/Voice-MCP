//go:build !windows

package audio

import "os/exec"

func hideConsole(*exec.Cmd) {}
