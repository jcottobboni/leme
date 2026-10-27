//go:build !windows

package plugins

import (
	"os/exec"
	"syscall"
	"time"
)

// ptyAttrs starts the command as the leader of a new session with the terminal as its controlling
// tty; killing the session's process group ends everything it started.
func ptyAttrs(cmd *exec.Cmd) *syscall.SysProcAttr {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
}
