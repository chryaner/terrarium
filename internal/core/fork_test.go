package core

import (
	"errors"
	"strings"
	"testing"
)

// The states a machine has to be dragged out of before modifyvm or
// unregistervm will touch it. aborted-saved is VirtualBox 7's word for a VM
// process that died holding a saved state. Discarding throws a RAM image away,
// so anything else must be left alone.
func TestNeedsDiscard(t *testing.T) {
	for _, s := range []string{"saved", "Saved", " saved\r", "aborted-saved", "Aborted-Saved"} {
		if !needsDiscard(s) {
			t.Errorf("a clone in state %q must be discarded before modifyvm", s)
		}
	}
	for _, s := range []string{"poweroff", "running", "aborted", "savingstate", ""} {
		if needsDiscard(s) {
			t.Errorf("state %q is not a saved state: discarding it would throw away RAM for nothing", s)
		}
	}
}

// The rollback is a side effect; the failure that triggered it is the thing
// the user has to act on, so it must survive both wrapping and unwrapping.
func TestRollbackErrKeepsCause(t *testing.T) {
	cause := errors.New("sshd never answered on port 42201")
	err := rollbackErr(cause, "trr-dev", nil)
	if !errors.Is(err, cause) {
		t.Errorf("cause must stay unwrappable: %v", err)
	}
	if !strings.Contains(err.Error(), "sshd never answered on port 42201") {
		t.Errorf("cause must stay readable: %q", err)
	}
}

// Cleanup that failed has to name what is left and how to delete it, without
// burying the original failure.
func TestRollbackErrReportsLeftovers(t *testing.T) {
	cause := errors.New("modifyvm: machine is not mutable")
	err := rollbackErr(cause, "trr-dev", []string{"unregistervm: already locked"})
	if !errors.Is(err, cause) {
		t.Errorf("cause must stay unwrappable: %v", err)
	}
	for _, want := range []string{
		"modifyvm: machine is not mutable",
		"unregistervm: already locked",
		"VBoxManage unregistervm trr-dev --delete-all",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %q", want, err)
		}
	}
}

// Every check that can fail cheaply fires before the clone, so a nil driver
// proves nothing touched VirtualBox first: a rejected fork has nothing to roll
// back.
func TestForkChecksComeBeforeVirtualBox(t *testing.T) {
	e := &Engine{St: promoteState()}
	nop := func(string) {}
	cases := []struct {
		name           string
		golden, envArg string
		wantErr        string
	}{
		{"missing golden", "nope", "x", "no golden"},
		{"bad env name", "debian-12", "team dev", "invalid env name"},
		{"existing env", "debian-12", "dev", "already exists"},
	}
	for _, c := range cases {
		env, err := e.Fork(c.golden, c.envArg, ForkOpts{}, nop)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.wantErr)
		}
		if env != nil {
			t.Errorf("%s: a failed fork must return no env: %+v", c.name, env)
		}
	}
}
