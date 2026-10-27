//go:build !windows

package plugins

import (
	"os/exec"
	"syscall"
	"time"
)

// killGroup runs the command in its own process group and kills the whole group on cancel, so
// children (kubectl started by a shell script, a pager…) do not outlive the run.
func killGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if !cmd.SysProcAttr.Setsid {
		cmd.SysProcAttr.Setpgid = true
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
}
