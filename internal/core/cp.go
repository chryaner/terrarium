package core

import (
	"fmt"
	"strings"

	"github.com/chryaner/terrarium/internal/sshx"
)

// copySpec is one side of a cp: a path on this host, or a path inside an env.
type copySpec struct {
	Env  string // empty means the host
	Path string
}

// parseCopySpec reads scp's `<env>:<path>` notation. The awkward case is a
// Windows host path: `C:\src` splits on a colon exactly like an env spec
// does, so a single character before the colon is always a drive letter and
// never an env. That costs one-letter env names their spec, which is the
// right trade on a Windows-only host. Everything after the first colon is the
// path, so `t1:C:/Users/x` names a Windows guest path.
func parseCopySpec(s string) copySpec {
	name, rest, ok := strings.Cut(s, ":")
	if !ok || len(name) < 2 || !nameRe.MatchString(name) {
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
	s, d := parseCopySpec(src), parseCopySpec(dst)
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

// Copy moves a file or directory between the host and an env over SFTP, using
// the credentials the env's golden already holds - the same ones `exec` uses,
// so anything reachable by exec is reachable by cp.
func (e *Engine) Copy(src, dst string, recursive, parents bool) error {
	plan, err := planCopy(src, dst)
	if err != nil {
		return err
	}
	if plan.Push {
		return e.Push(plan.Env, plan.Local, plan.Remote, recursive, parents)
	}
	return e.Pull(plan.Env, plan.Remote, plan.Local, recursive, parents)
}

// Push copies a host path into an env.
func (e *Engine) Push(envName, local, remote string, recursive, parents bool) error {
	transport, err := e.EnvTransport(envName)
	if err != nil {
		return err
	}
	if transport == TransportGuestControl {
		// Guest Additions create the destination's parents themselves, so
		// there is no -p to honour and nothing to do differently without it.
		return e.copyGuestControl(envName, local, remote, true, recursive)
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}
	return sshx.PushTo(port, user, password, key, local, remote, recursive, parents)
}

// Pull copies a path out of an env to the host.
func (e *Engine) Pull(envName, remote, local string, recursive, parents bool) error {
	transport, err := e.EnvTransport(envName)
	if err != nil {
		return err
	}
	if transport == TransportGuestControl {
		return e.copyGuestControl(envName, local, remote, false, recursive)
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}
	return sshx.PullFrom(port, user, password, key, remote, local, recursive, parents)
}
