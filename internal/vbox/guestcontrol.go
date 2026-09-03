package vbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Guest Additions are the other way into a guest: VBoxService runs inside it
// and takes commands from the host over the hypervisor, so a Windows old
// enough to have no SSH server (XP, 7) is still reachable. It is slower than
// SSH - every call is a VBoxManage process and a new guest logon - and it
// needs the guest's own credentials, which appear on the VBoxManage command
// line where the host's process list can see them. That is documented; it is
// also why SSH stays the default.

// GuestCreds are the guest account guestcontrol logs in as. Password auth is
// the only thing Guest Additions offer.
type GuestCreds struct {
	User     string
	Password string
}

// guestRunArgs builds the argument list for one guestcontrol run. argv is what
// the program receives from argv[1] on: VBoxManage sets argv[0] to the image
// itself, so passing the program name again would make it the first argument.
func guestRunArgs(vm string, creds GuestCreds, timeout time.Duration, exe string, argv []string) []string {
	args := []string{"guestcontrol", vm, "run",
		"--username", creds.User,
		"--password", creds.Password,
		"--wait-stdout", "--wait-stderr",
		"--timeout", strconv.FormatInt(timeout.Milliseconds(), 10),
		"--exe", exe, "--"}
	return append(args, argv...)
}

// VBoxManage reports a guest exit code as its own, offset by 32 and clamped:
// everything from 94 up comes back as 126, and the codes below 32 are
// VBoxManage's own failures. Verified against VirtualBox 7.2.
const (
	guestExitBase = 32
	guestExitMax  = 126
)

// guestExitCode reads what a finished `guestcontrol run` exited with. ok is
// false when the code is VBoxManage failing rather than the guest program
// exiting - bad credentials, no additions, a session that would not start -
// which is an error for the caller, not a result.
//
// The exit code is the only clean channel for this: `--verbose` does print the
// guest's own exit code, but on the same stdout as the guest's output, and
// mixing VBoxManage's narration into a command's output is worse than losing
// the distinction between exit 94 and exit 200.
func guestExitCode(vboxExit int) (code int, ok bool) {
	switch {
	case vboxExit == 0:
		return 0, true
	case vboxExit == guestExitMax:
		// Saturated: the guest failed, and by how much is not recoverable.
		return guestExitMax, true
	case vboxExit > guestExitBase && vboxExit < guestExitMax:
		return vboxExit - guestExitBase, true
	}
	return -1, false
}

// GuestTimeout reports a guest command that outlived its deadline. Like an
// SSH session, a guest process cannot be called back: VBoxManage stops waiting
// and the command carries on running inside the guest, which is what the
// caller has to be told so it can go and kill it.
type GuestTimeout struct {
	Timeout time.Duration
}

func (e *GuestTimeout) Error() string {
	return fmt.Sprintf("guest command did not finish within %s and is still running in the guest", e.Timeout)
}

const (
	// timedOutMarker is what VBoxManage prints when its own --timeout fires.
	// It exits 1 either way, so the message is the only thing that tells a
	// deadline apart from a session that would not start.
	timedOutMarker = "VERR_TIMEOUT"
	// tailBytes is how much of a stream tailWriter keeps. Enough for the last
	// few VBoxManage messages, which is all anything reads it for.
	tailBytes = 4096
	// guestKillGrace is how long VirtualBox's own timeout runs past the host's,
	// so a caller that wants to kill the tree gets there first and one that
	// does not still has the process ended for it.
	guestKillGrace = 60 * time.Second
)

// tailWriter passes everything through and keeps the last tailBytes, so a
// stream going to the caller can still be looked at for one known message.
type tailWriter struct {
	w   io.Writer
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBytes {
		t.buf = t.buf[len(t.buf)-tailBytes:]
	}
	if t.w == nil {
		return len(p), nil
	}
	return t.w.Write(p)
}

// GuestRun runs one program in the guest through Guest Additions and returns
// its exit code.
func (c *Client) GuestRun(ctx context.Context, vm string, creds GuestCreds, timeout time.Duration, exe string, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	// The deadline is the host's, and VirtualBox's own is a backstop behind it.
	// The other way round, VirtualBox terminates the guest process the moment
	// the deadline passes and whatever that process started is orphaned with
	// nothing left carrying the marker - so a caller that wanted to kill the
	// tree finds nothing to kill. Killing VBoxManage instead leaves the guest
	// process running, which is what a kill-on-timeout needs to find, and the
	// backstop ends it anyway for a caller that did not ask for one.
	hostCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tail := &tailWriter{w: stderr}
	cmd := exec.CommandContext(hostCtx, c.Exe, guestRunArgs(vm, creds, timeout+guestKillGrace, exe, argv)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = tail
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if hostCtx.Err() != nil || strings.Contains(string(tail.buf), timedOutMarker) {
		return -1, &GuestTimeout{Timeout: timeout}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code, ok := guestExitCode(ee.ExitCode()); ok {
			return code, nil
		}
	}
	// The password is on the command line, so the argv never goes in an error.
	return -1, fmt.Errorf("VBoxManage guestcontrol run on %s: %w", vm, err)
}

// GuestMkdirAll creates a directory in the guest, parents included. copyto
// will not do it: it fails with "path to guest file not found" rather than
// creating what is missing, verified against VirtualBox 7.2.
func (c *Client) GuestMkdirAll(vm string, creds GuestCreds, guestDir string) error {
	out, err := c.runRaw(DefaultTimeout, "guestcontrol", vm, "mkdir",
		"--username", creds.User, "--password", creds.Password,
		"--parents", guestWindowsPath(guestDir))
	if err != nil {
		return fmt.Errorf("VBoxManage guestcontrol mkdir on %s: %s", vm, redactedTail(out))
	}
	return nil
}

// GuestCopyTo copies one host file into the guest. The positional form names
// the destination file exactly, which --target-directory cannot.
func (c *Client) GuestCopyTo(vm string, creds GuestCreds, hostPath, guestPath string, recursive bool) error {
	return c.guestCopy(vm, creds, "copyto", hostPath, guestWindowsPath(guestPath), recursive)
}

// GuestCopyFrom copies one guest file out to the host.
func (c *Client) GuestCopyFrom(vm string, creds GuestCreds, guestPath, hostPath string, recursive bool) error {
	return c.guestCopy(vm, creds, "copyfrom", guestWindowsPath(guestPath), hostPath, recursive)
}

func (c *Client) guestCopy(vm string, creds GuestCreds, verb, src, dst string, recursive bool) error {
	args := []string{"guestcontrol", vm, verb,
		"--username", creds.User, "--password", creds.Password}
	if recursive {
		args = append(args, "--recursive")
	}
	args = append(args, src, dst)
	out, err := c.runRaw(slowTimeout, args...)
	if err != nil {
		return fmt.Errorf("VBoxManage guestcontrol %s on %s: %s", verb, vm, redactedTail(out))
	}
	return nil
}

// guestWindowsPath converts terrarium's forward-slash guest paths to the
// separator the copy verbs want. A POSIX guest has no backslashes to convert,
// and a path with none is left alone either way.
func guestWindowsPath(p string) string {
	if len(p) > 1 && p[1] == ':' {
		return strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// redactedLines is how much of a failed call's output an error quotes. The
// first few lines carry what went wrong; the rest is VBoxManage's context
// trace.
const redactedLines = 4

// redactedTail is what a failed guestcontrol call may be quoted as. VBoxManage
// echoes nothing of its own arguments in these messages, but the credentials
// are on that command line, so only the message VirtualBox produced is kept
// and it is never the argv.
func redactedTail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	if len(lines) > redactedLines {
		lines = lines[:redactedLines]
	}
	return strings.Join(lines, "; ")
}

// GuestAdditionsVersion reports what the guest's additions announce, or "" if
// they have not reported in yet. A running guest with no additions - or one
// still booting - answers nothing rather than failing.
func (c *Client) GuestAdditionsVersion(vm string) (string, error) {
	out, err := c.run("guestproperty", "get", vm, "/VirtualBox/GuestAdd/Version")
	if err != nil {
		return "", err
	}
	v, ok := strings.CutPrefix(strings.TrimSpace(out), "Value:")
	if !ok {
		// "No value set!" - additions are not up.
		return "", nil
	}
	return strings.TrimSpace(v), nil
}

const (
	// readyProbeTimeout bounds one trial command. A guest still starting
	// services refuses fast; one that hangs is not ready either.
	readyProbeTimeout = 30 * time.Second
	// readyPollInterval is the gap between attempts, against a boot measured
	// in tens of seconds.
	readyPollInterval = 2 * time.Second
)

// GuestReady blocks until the guest can actually run a command: the additions
// have reported a version and a trial command comes back. The version alone is
// not enough - it appears while the guest is still starting services, and the
// first real command then fails with a session error.
func (c *Client) GuestReady(ctx context.Context, vm string, creds GuestCreds, exe string, argv []string, timeout time.Duration, progress func(string)) error {
	deadline := time.Now().Add(timeout)
	said := false
	for time.Now().Before(deadline) {
		v, err := c.GuestAdditionsVersion(vm)
		if err == nil && v != "" {
			if !said {
				progress("guest additions " + v + ", waiting for them to take a command")
				said = true
			}
			if _, err := c.GuestRun(ctx, vm, creds, readyProbeTimeout, exe, argv, nil, io.Discard, io.Discard); err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
	return fmt.Errorf("guest additions on %s did not answer a command within %s: are they installed and is the user %q able to log in?",
		vm, timeout, creds.User)
}
