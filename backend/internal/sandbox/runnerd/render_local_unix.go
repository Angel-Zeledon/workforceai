//go:build !windows

package runnerd

import (
	"os/exec"
	"syscall"
)

// setGroup puts the child in its own process group so the whole tree (the
// program and the poppler tools it starts) can be killed at once.
func setGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}
}
