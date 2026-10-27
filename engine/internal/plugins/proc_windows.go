//go:build windows

package plugins

import (
	"os/exec"
	"time"
)

// killGroup kills the process on cancel (Windows has no process groups in os/exec).
func killGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
