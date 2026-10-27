package plugins

import "io"

// Term is a running interactive command.
type Term interface {
	io.ReadWriter
	Resize(cols, rows uint16) error
	// Wait returns the exit code once the command ended.
	Wait() (int, error)
	// PTY reports whether the command has a real terminal (false: plain pipes, as on Windows).
	PTY() bool
	Close() error
}
