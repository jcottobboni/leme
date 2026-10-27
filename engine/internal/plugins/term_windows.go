//go:build windows

package plugins

import (
	"errors"
	"io"
	"os/exec"
)

// pipeTerm runs the command with plain pipes: no PTY on Windows (ConPTY would need another
// dependency). Line-oriented commands (kubectl logs -f, describe…) work; full-screen ones do not.
type pipeTerm struct {
	p    *Prepared
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *io.PipeReader
	outW *io.PipeWriter
}

// StartTerminal starts a prepared command with pipes.
func StartTerminal(p *Prepared, cols, rows uint16) (Term, error) {
	cmd := p.Cmd
	killGroup(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		p.Cleanup()
		return nil, err
	}
	r, w := io.Pipe()
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		p.Cleanup()
		return nil, err
	}
	return &pipeTerm{p: p, cmd: cmd, in: in, out: r, outW: w}, nil
}

func (t *pipeTerm) Read(b []byte) (int, error)     { return t.out.Read(b) }
func (t *pipeTerm) Write(b []byte) (int, error)    { return t.in.Write(b) }
func (t *pipeTerm) PTY() bool                      { return false }
func (t *pipeTerm) Resize(cols, rows uint16) error { return nil }

func (t *pipeTerm) Wait() (int, error) {
	defer t.p.Cleanup()
	err := t.cmd.Wait()
	_ = t.outW.Close()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (t *pipeTerm) Close() error {
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.in.Close()
	return t.outW.Close()
}
