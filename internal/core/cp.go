package core

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/chryaner/terrarium/internal/sshx"
)

// copySpec is one side of a cp: a path on this host, or a path inside an env.
type copySpec struct {
	Env  string // empty means the host
	Path string
}

// windowsHost switches on drive-letter parsing; a var so tests can pin it.
var windowsHost = runtime.GOOS == "windows"

// parseCopySpec reads scp's `<env>:<path>` notation. The awkward case is a
// Windows host path: `C:\src` splits on a colon exactly like an env spec
// does, so on a Windows host a single character before the colon is always a
// drive letter and never an env. Everything after the first colon is the
// path, so `t1:C:/Users/x` names a Windows guest path.
func parseCopySpec(s string, windows bool) copySpec {
	name, rest, ok := strings.Cut(s, ":")
	if !ok || !nameRe.MatchString(name) || (windows && len(name) < 2) {
		return copySpec{Path: s}
	}
	return copySpec{Env: name, Path: rest}
}

// copyPlan is a validated cp: which env, which path on each side, which way.
type copyPlan struct {
	Env    string
	Local  string
	Remote string
	Push   bool // host -> guest
}

// planCopy resolves the two specs into a direction. Exactly one side must
// name an env: the host cannot be one end of both halves of the transfer, and
// guest-to-guest would need the host in the middle anyway.
func planCopy(src, dst string) (copyPlan, error) {
	s, d := parseCopySpec(src, windowsHost), parseCopySpec(dst, windowsHost)
	switch {
	case s.Env == "" && d.Env == "":
		return copyPlan{}, fmt.Errorf("neither side names an env: use `terrarium cp <src> <env>:<path>` or `terrarium cp <env>:<path> <dst>`")
	case s.Env != "" && d.Env != "":
		return copyPlan{}, fmt.Errorf("both sides name an env (%s and %s): copy via the host, in two commands", s.Env, d.Env)
	}

	plan := copyPlan{Env: s.Env, Local: d.Path, Remote: s.Path}
	if s.Env == "" {
		plan = copyPlan{Env: d.Env, Local: s.Path, Remote: d.Path, Push: true}
	}
	if plan.Remote == "" {
		return copyPlan{}, fmt.Errorf("no path after %s:", plan.Env)
	}
	if plan.Local == "" {
		return copyPlan{}, fmt.Errorf("no path on the host side")
	}
	return plan, nil
}

// StdStream on the host side of a cp means standard input or output, as it
// does for tar and cat. It is how a file crosses from a third machine in one
// hop: `ssh host terrarium cp - env:/path < file`.
const StdStream = "-"

// Copy moves a file or directory between the host and an env over SFTP, using
// the credentials the env's golden already holds - the same ones `exec` uses,
// so anything reachable by exec is reachable by cp. stdin and stdout are what
// a host side of "-" reads and writes.
func (e *Engine) Copy(src, dst string, recursive, parents bool, stdin io.Reader, stdout io.Writer) error {
	plan, err := planCopy(src, dst)
	if err != nil {
		return err
	}
	if plan.Local == StdStream {
		if recursive {
			return fmt.Errorf("-r cannot be used with %s: a stream carries one file. Pipe a tar archive and unpack it in the guest", StdStream)
		}
		if plan.Push {
			return e.PushStream(plan.Env, stdin, plan.Remote, parents)
		}
		return e.PullStream(plan.Env, plan.Remote, stdout)
	}
	if plan.Push {
		return e.Push(plan.Env, plan.Local, plan.Remote, recursive, parents)
	}
	return e.Pull(plan.Env, plan.Remote, plan.Local, recursive, parents)
}

// Push copies a host path into an env.
func (e *Engine) Push(envName, local, remote string, recursive, parents bool) error {
	transport, err := e.envTransport(envName)
	if err != nil {
		return err
	}
	if transport == TransportGuestControl {
		return e.copyGuestControl(envName, local, remote, true, recursive, parents)
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}
	return sshx.PushTo(port, user, password, key, local, remote, recursive, parents)
}

// Pull copies a path out of an env to the host.
func (e *Engine) Pull(envName, remote, local string, recursive, parents bool) error {
	transport, err := e.envTransport(envName)
	if err != nil {
		return err
	}
	if transport == TransportGuestControl {
		return e.copyGuestControl(envName, local, remote, false, recursive, parents)
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}
	return sshx.PullFrom(port, user, password, key, remote, local, recursive, parents)
}

// PushStream writes a stream to one file in an env.
func (e *Engine) PushStream(envName string, r io.Reader, remote string, parents bool) error {
	transport, err := e.envTransport(envName)
	if err != nil {
		return err
	}
	if transport == TransportGuestControl {
		// Guest Additions copy files, not streams, so the stream is spooled
		// to a host file first.
		tmp, err := spool(r)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		return e.copyGuestControl(envName, tmp, remote, true, false, parents)
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}
	return sshx.PushStream(port, user, password, key, r, remote, parents)
}

// PullStream writes one file of an env to a stream.
func (e *Engine) PullStream(envName, remote string, w io.Writer) error {
	transport, err := e.envTransport(envName)
	if err != nil {
		return err
	}
	if transport == TransportGuestControl {
		tmp, err := spool(strings.NewReader(""))
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		if err := e.copyGuestControl(envName, tmp, remote, false, false, false); err != nil {
			return err
		}
		f, err := os.Open(tmp)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}
	return sshx.PullStream(port, user, password, key, remote, w)
}

// spool writes a stream to a temporary host file and returns its path.
func spool(r io.Reader) (string, error) {
	f, err := os.CreateTemp("", "terrarium-cp-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}
