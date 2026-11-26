package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// probeTTL marks a probe env for `terrarium gc`, in case the check dies
// before it can remove the env itself.
const probeTTL = 30 * time.Minute

// probeTimeout bounds the one command a probe runs.
const probeTimeout = 2 * time.Minute

// CheckGoldens proves each named golden still forks into an env a command
// runs in, the only way to find a golden that has gone bad while switched
// off: a Windows password that expired, a login that no longer matches the
// record, a disk that no longer boots. With no names it checks every golden.
// Each check forks a throwaway env and removes it again, and is handed to
// report as it finishes: a run over many goldens takes minutes.
func (e *Engine) CheckGoldens(names []string, report func(Check)) []Check {
	if len(names) == 0 {
		for name := range e.St.Goldens {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	checks := make([]Check, 0, len(names))
	for _, name := range names {
		c := e.checkGolden(name)
		report(c)
		checks = append(checks, c)
	}
	return checks
}

func (e *Engine) checkGolden(name string) Check {
	c := Check{Name: "golden " + name}
	g := e.St.Goldens[name]
	switch {
	case g == nil:
		c.Detail = "no such golden"
		c.Fix = "`terrarium ls --goldens` lists the ones there are"
		return c
	case transportOf(g) == TransportSSH && !g.hasCreds(),
		transportOf(g) == TransportGuestControl && (g.SSHUser == "" || g.SSHPassword == ""):
		// Nothing to prove: terrarium never logs in to these, by design.
		c.OK, c.Optional = true, true
		c.Detail = "no login recorded, driven through the console: not checked"
		return c
	}

	env := probeEnvName(name)
	start := time.Now()
	quiet := func(string) {}
	if _, err := e.Fork(name, env, ForkOpts{TTL: probeTTL}, quiet); err != nil {
		c.Detail = "fork failed: " + err.Error()
		c.Fix = goldenFix(name, g)
		return c
	}
	shell, err := e.ShellFor(env)
	if err == nil {
		noop := "exit 0"
		if shell == ShellPOSIX {
			noop = "true"
		}
		err = e.runQuiet(env, probeTimeout, noop)
	}
	if rmErr := e.Remove(env); rmErr != nil && err == nil {
		c.Detail = fmt.Sprintf("forked and ran a command, but the probe env %s could not be removed: %v", env, rmErr)
		c.Fix = "`terrarium rm " + env + "`"
		return c
	}
	if err != nil {
		// The first line only: the rest of a refused login is a pointer back
		// to this very check.
		reason, _, _ := strings.Cut(err.Error(), "\n")
		c.Detail = "forked, but no command runs in it: " + reason
		c.Fix = goldenFix(name, g)
		return c
	}
	c.OK = true
	c.Detail = fmt.Sprintf("forks and runs a command (%.0fs)", time.Since(start).Seconds())
	return c
}

// goldenFix is what to do about a golden that failed its check. A Windows
// golden gets the expired-password story, because that is what breaks one
// that used to work and nothing else says so.
func goldenFix(name string, g *Golden) string {
	if isWindowsGuest(g.OSType) {
		return "if it used to work, its Windows password has probably expired (Windows does that after 42 days and then refuses the login, key or not): " +
			"fork it, set the password on the console (`terrarium show <env>`), run `net accounts /maxpwage:unlimited` in the guest, " +
			"then `terrarium promote <env> <image>` and `terrarium adopt` it with the new password. Or rebuild it: `terrarium get " + name + " --force`"
	}
	return "fork it and look at the console (`terrarium show <env>`), or rebuild it: `terrarium get " + name + " --force`"
}

// probeEnvName is a throwaway env name for checking a golden: recognisable in
// a listing, a legal env name whatever the golden is called, and unlikely to
// collide with a probe left over from a run that died.
func probeEnvName(golden string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		}
		return '-'
	}, golden)
	var b [3]byte
	rand.Read(b[:])
	return "doctor-" + strings.Trim(safe, "-") + "-" + hex.EncodeToString(b[:])
}
