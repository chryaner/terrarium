package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// OutputBuffer collects a command's output. The ssh client copies stdout and
// stderr on separate goroutines, so passing one plain bytes.Buffer as both
// would race.
type OutputBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *OutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String is safe to call while the command is still running.
func (b *OutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TimeoutError reports a command that outlived its deadline. The SSH session
// cannot be cancelled from the client, so the guest is still running it.
type TimeoutError struct {
	Timeout time.Duration
	Command string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("command did not finish within %s and is probably still running in the guest: %s",
		e.Timeout, e.Command)
}

// ExecScript is ExecStreams with a ceiling on how long to wait and an optional
// script fed to the command's own stdin, for the shells that read one there:
// `sh -s`, `powershell -Command -`. That is the one way to hand a guest a
// multi-line script without it passing through anybody's quoting on the way.
//
// Whatever the command printed before the deadline has already been written to
// stdout and stderr, which is the only thing left to go on when it hangs.
func ExecScript(ctx context.Context, timeout time.Duration, port int, user, password, keyPath, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := execStreams(port, user, password, keyPath, command, stdin, stdout, stderr)
		done <- result{code, err}
	}()

	select {
	case r := <-done:
		return r.code, r.err
	case <-ctx.Done():
		// The goroutine is left to finish and drain into stdout/stderr; there
		// is no way to interrupt an in-flight ssh session from here.
		return -1, &TimeoutError{Timeout: timeout, Command: command}
	}
}

// Exec runs a command in the guest and returns its exit code.
func Exec(port int, user, password, keyPath, command string) (int, error) {
	return ExecStreams(port, user, password, keyPath, command, os.Stdout, os.Stderr)
}

// ExecStreams is Exec with the guest's output redirected, so callers that
// need to inspect it (first-boot orchestration) can.
func ExecStreams(port int, user, password, keyPath, command string, stdout, stderr io.Writer) (int, error) {
	return execStreams(port, user, password, keyPath, command, nil, stdout, stderr)
}

func execStreams(port int, user, password, keyPath, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	client, err := Dial(port, user, password, keyPath)
	if err != nil {
		return -1, err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return -1, err
	}
	defer sess.Close()

	// A nil stdin stays nil. Defaulting it to os.Stdin would make the ssh
	// client copy from the JSON-RPC transport under `terrarium mcp`, so the
	// first exec would eat protocol frames; script mode passes the script it
	// wants read and nothing else. Interactive shells go through ssh.exe in
	// cli/sshcmd.go.
	if stdin != nil {
		sess.Stdin = stdin
	}
	sess.Stdout = stdout
	sess.Stderr = stderr

	err = sess.Run(command)
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
