package cli

import (
	"strings"
	"testing"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/sshx"
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
			// Base64 rather than a quoted command line: cmd.exe splits on &
			// inside every quoting it has, so `-Command 'a&whoami'` runs
			// whoami in cmd.
			"forcing PowerShell on a cmd guest encodes the command",
			core.ShellPowerShell, core.ShellCmd, argv,
			"powershell -NoProfile -NonInteractive -EncodedCommand " +
				sshx.EncodePowerShell(`Get-Item 'C:\Program Files'`),
		},
		{
			"forcing cmd on a PowerShell guest launches it",
			core.ShellCmd, core.ShellPowerShell, []string{"dir", "/b"},
			"cmd /c 'dir /b'",
		},
		{
			// The hole this closes: unquoted, PowerShell expands the
			// variable on the guest's login shell and cmd.exe is handed the
			// answer.
			"a cmd command on a PowerShell guest is one PowerShell argument",
			core.ShellCmd, core.ShellPowerShell, []string{"echo", "$env:USERNAME"},
			`cmd /c 'echo $env:USERNAME'`,
		},
		{
			"a cmd command's own metacharacters do not reach PowerShell",
			core.ShellCmd, core.ShellPowerShell, []string{"echo", "a&whoami|more", "$(id)"},
			`cmd /c 'echo a&whoami|more $(id)'`,
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
		{
			"forcing sh on a PowerShell guest passes the script as one argument",
			core.ShellPOSIX, core.ShellPowerShell, []string{"echo", "$HOME;id"},
			`sh -c 'echo ''$HOME;id'''`,
		},
	}
	for _, c := range cases {
		if got := execCommand(c.want, c.have, c.argv); got != c.out {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.out)
		}
	}
}

// Nothing a wrapped command contains may be read by the shell that carries
// the line: that shell runs with the same rights as the command, so a $( ) or
// an & that leaks out of the wrapper is a command the caller did not ask for.
func TestExecCommandDoesNotLeakMetacharacters(t *testing.T) {
	nasty := []string{"echo", "a&b", "$(id)", "`whoami`", "x|y", "p;q"}
	for _, c := range []struct {
		name       string
		want, have string
		// leaks are the substrings that must not appear bare in the line the
		// carrying shell parses.
		leaks []string
	}{
		{"cmd on a PowerShell guest", core.ShellCmd, core.ShellPowerShell, []string{"$(id)", "`whoami`"}},
		{"PowerShell on a cmd guest", core.ShellPowerShell, core.ShellCmd, []string{"a&b", "x|y"}},
	} {
		line := execCommand(c.want, c.have, nasty)
		for _, leak := range c.leaks {
			// Quoted is fine; PowerShell's own quoting is single quotes, and
			// the encoded form contains none of it at all.
			if c.have == core.ShellCmd && strings.Contains(line, leak) {
				t.Errorf("%s: %q reaches cmd.exe unencoded in %s", c.name, leak, line)
			}
			if c.have == core.ShellPowerShell && !strings.Contains(line, "'") {
				t.Errorf("%s: nothing is quoted in %s", c.name, line)
			}
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
