package core

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
)

// ExecRequest is one command to run inside an env. Building it is the CLI's
// and the MCP server's job - they decide the quoting - and running it is this
// package's, so both get the same timeout, marker and kill behaviour.
type ExecRequest struct {
	Env string
	// Command is the whole command line the guest's session carries.
	Command string
	// Stdin, when set, is the script Command reads on its own stdin.
	Stdin io.Reader
	// GuestShell is what the guest's own session lands in. It decides how the
	// marker is spelled, so it is needed even when Command was quoted for
	// some other shell.
	GuestShell string
	Timeout    time.Duration
	// KillOnTimeout tags the command and, if the deadline passes, opens a
	// second session to kill everything the tag names.
	KillOnTimeout bool
	// Desktop runs the command in the session a user is logged into rather
	// than the service session sshd hands it. Windows guests only.
	Desktop        bool
	Stdout, Stderr io.Writer
}

// Run executes one command line in an env and returns the guest's exit code.
// It is the single place a command crosses into a guest, so the transport the
// golden records is applied once, here.
func (e *Engine) Run(ctx context.Context, envName string, timeout time.Duration, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	transport, err := e.EnvTransport(envName)
	if err != nil {
		return -1, err
	}
	if transport == TransportGuestControl {
		return e.runGuestControl(ctx, envName, timeout, command, stdin, stdout, stderr)
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return -1, err
	}
	return sshx.ExecScript(ctx, timeout, port, user, password, key, command, stdin, stdout, stderr)
}

// Exec runs a request: marker injection, the run itself, and the kill that a
// timeout triggers when the caller asked for one.
func (e *Engine) Exec(ctx context.Context, r ExecRequest) (int, error) {
	if r.Desktop {
		return e.execDesktop(ctx, r)
	}
	command := r.Command
	id := ""
	if r.KillOnTimeout {
		id = NewMarkerID()
		command = MarkCommand(r.GuestShell, command, id)
	}
	code, err := e.Run(ctx, r.Env, r.Timeout, command, r.Stdin, r.Stdout, r.Stderr)
	var timedOut *sshx.TimeoutError
	if id != "" && errors.As(err, &timedOut) {
		return code, e.killMarked(r.Env, r.GuestShell, id, r.Timeout, r.Command)
	}
	return code, err
}
