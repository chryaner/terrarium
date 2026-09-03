package cli

import (
	"testing"

	"github.com/chryaner/terrarium/internal/core"
)

// The command line an SSH session carries, for every combination of the shell
// that has to read the command and the shell sshd will actually hand it to.
// The second one matters because it parses the line first: a wrapper that
// looks right on paper loses its quotes on the way through.
func TestExecCommand(t *testing.T) {
	argv := []string{"Get-Item", `C:\Program Files`}
	cases := []struct {
		name string
		want string
		have string
		argv []string
		out  string
	}{
		{
			"a PowerShell guest reads PowerShell quoting directly",
			core.ShellPowerShell, core.ShellPowerShell, argv,
			`Get-Item 'C:\Program Files'`,
		},
		{
			"a Linux guest reads POSIX quoting directly",
			core.ShellPOSIX, core.ShellPOSIX, []string{"grep", "a|b", "f"},
			`grep 'a|b' f`,
		},
		{
			// cmd cannot rebuild an argv from any quoting, so it gets none.
			"a cmd guest gets the words as typed",
			core.ShellCmd, core.ShellCmd, []string{"dir", `C:\Program Files`},
			`dir C:\Program Files`,
		},
		{
			"forcing PowerShell on a cmd guest launches it",
			core.ShellPowerShell, core.ShellCmd, argv,
			`powershell -NoProfile -NonInteractive -Command Get-Item 'C:\Program Files'`,
		},
		{
			"forcing cmd on a PowerShell guest launches it",
			core.ShellCmd, core.ShellPowerShell, []string{"dir", "/b"},
			"cmd /c dir /b",
		},
		{
			// sh -c takes its script as one argument, so the quoted command
			// is quoted again on the way in.
			"forcing sh wraps the whole command as one argument",
			core.ShellPOSIX, core.ShellCmd, []string{"echo", "a b"},
			`sh -c 'echo '\''a b'\'''`,
		},
		{
			// The shell the golden records is only a guess for a guest that
			// has moved on, and forcing it must not then double-wrap.
			"forcing the shell the guest already runs changes nothing",
			core.ShellPowerShell, core.ShellPowerShell, []string{"Write-Output", "$x"},
			`Write-Output '$x'`,
		},
	}
	for _, c := range cases {
		if got := execCommand(c.want, c.have, c.argv); got != c.out {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.out)
		}
	}
}

// --shell names the shell the way a user thinks of it; the state file names it
// the way a golden records it.
func TestShellFlag(t *testing.T) {
	for in, want := range map[string]string{
		"":           "",
		"sh":         core.ShellPOSIX,
		"posix":      core.ShellPOSIX,
		"cmd":        core.ShellCmd,
		"powershell": core.ShellPowerShell,
	} {
		got, err := shellFlag(in)
		if err != nil {
			t.Errorf("--shell %q: %v", in, err)
		}
		if got != want {
			t.Errorf("--shell %q: got %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"bash", "pwsh", "PowerShell"} {
		if _, err := shellFlag(bad); err == nil {
			t.Errorf("--shell %q should be rejected: quoting for a shell that is not there fails silently", bad)
		}
	}
}
