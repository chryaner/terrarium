package core

import (
	"errors"
	"testing"
)

// What the two Windows shells actually print for `echo %COMSPEC%`. Getting
// this wrong quotes every later command for the wrong shell, so it is worth
// pinning the real answers rather than the code's own idea of them.
func TestClassifyShellProbe(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"cmd expands the variable", "C:\\Windows\\system32\\cmd.exe\r\n", ShellCmd},
		{"a 32-bit guest answers with its own path", "C:\\WINDOWS\\SysWOW64\\cmd.exe\n", ShellCmd},
		{"powershell has no % expansion", "%COMSPEC%\r\n", ShellPowerShell},
		{"leading and trailing space is not an answer", "  %COMSPEC%  ", ShellPowerShell},
		// A guest that answered with neither is not worth guessing about.
		{"an empty answer is no answer", "", ""},
		{"an error message is no answer", "'echo' is not recognized\r\n", ""},
	}
	for _, c := range cases {
		if got := classifyShellProbe(c.out); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Only Windows costs a round trip. Everything else is settled by the guest
// type, and an unanswerable Windows guest stays unrecorded rather than being
// guessed at.
func TestProbeShell(t *testing.T) {
	asked := false
	ask := func(out string, err error) func() (string, error) {
		return func() (string, error) {
			asked = true
			return out, err
		}
	}

	asked = false
	if got := probeShell("Ubuntu (64-bit)", ask("%COMSPEC%", nil)); got != ShellPOSIX {
		t.Errorf("a Linux guest should be posix, got %q", got)
	}
	if asked {
		t.Error("a Linux guest should not be asked: its type already says posix")
	}

	if got := probeShell("Windows 10 (64-bit)", ask("%COMSPEC%\r\n", nil)); got != ShellPowerShell {
		t.Errorf("got %q, want %q", got, ShellPowerShell)
	}
	if got := probeShell("Windows10_64", ask("C:\\Windows\\system32\\cmd.exe\r\n", nil)); got != ShellCmd {
		t.Errorf("got %q, want %q", got, ShellCmd)
	}
	// Both mean "ask again later", not "assume something".
	if got := probeShell("Windows10_64", nil); got != "" {
		t.Errorf("an unreachable guest should stay unrecorded, got %q", got)
	}
	if got := probeShell("Windows10_64", ask("", errors.New("connection refused"))); got != "" {
		t.Errorf("a failed probe should stay unrecorded, got %q", got)
	}
}
