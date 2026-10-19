//go:build windows

package runnerd

import "os/exec"

// Windows has no process groups here; the local mode is a Linux container
// feature and this keeps the build (and the unit tests) working.
func setGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
