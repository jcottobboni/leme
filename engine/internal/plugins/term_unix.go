//go:build !windows

package plugins

import (
	"errors"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type ptyTerm struct {
	p   *Prepared
	f   *os.File
	cmd *exec.Cmd
}

// StartTerminal starts a prepared command on a pseudo-terminal.
func StartTerminal(p *Prepared, cols, rows uint16) (Term, error) {
	cmd := p.Cmd
	f, err := pty.StartWithAttrs(cmd, &pty.Winsize{Cols: cols, Rows: rows}, ptyAttrs(cmd))
	if err != nil {
		p.Cleanup()
		return nil, err
	}
	return &ptyTerm{p: p, f: f, cmd: cmd}, nil
}

func (t *ptyTerm) Read(b []byte) (int, error)  { return t.f.Read(b) }
func (t *ptyTerm) Write(b []byte) (int, error) { return t.f.Write(b) }
func (t *ptyTerm) PTY() bool                   { return true }

func (t *ptyTerm) Resize(cols, rows uint16) error {
	return pty.Setsize(t.f, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *ptyTerm) Wait() (int, error) {
	defer t.p.Cleanup()
	err := t.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (t *ptyTerm) Close() error {
	if t.cmd.Cancel != nil {
		_ = t.cmd.Cancel()
	}
	return t.f.Close()
}
