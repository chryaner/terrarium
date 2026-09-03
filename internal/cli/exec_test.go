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
	nasty := []string{"echo", "a&b", "$(id)", "\\`whoami`", "x|y", "p;q"}

	// On a cmd guest the wrapper is base64, so none of it is on the line at
	// all - not quoted, not escaped, absent.
	encoded := execCommand(core.ShellPowerShell, core.ShellCmd, nasty)
	for _, leak := range []string{"a&b", "x|y", "p;q", "$(id)"} {
		if strings.Contains(encoded, leak) {
			t.Errorf("%q reaches cmd.exe unencoded in %s", leak, encoded)
		}
	}
	// And it is really the command, not something lost on the way.
	if !strings.Contains(encoded, sshx.EncodePowerShell(sshx.QuotePowerShell(nasty))) {
		t.Errorf("the encoded payload is not the quoted command: %s", encoded)
	}

	// On a PowerShell guest the wrapper is one single-quoted argument, so the
	// metacharacters are inside it and PowerShell hands them to cmd.exe
	// verbatim. Unquoting has to give back exactly what was passed.
	line := execCommand(core.ShellCmd, core.ShellPowerShell, nasty)
	arg, ok := strings.CutPrefix(line, "cmd /c ")
	if !ok {
		t.Fatalf("not a cmd wrapper: %s", line)
	}
	if !strings.HasPrefix(arg, "'") || !strings.HasSuffix(arg, "'") {
		t.Fatalf("the command is not one quoted argument: %s", line)
	}
	unquoted := strings.ReplaceAll(arg[1:len(arg)-1], "''", "'")
	if want := strings.Join(nasty, " "); unquoted != want {
		t.Errorf("what cmd.exe receives is not what was asked for:\n got %s\nwant %s", unquoted, want)
	}
	// A lone quote inside the argument would end it and leave the rest as
	// PowerShell syntax, so every one of them has to be doubled.
	for _, i := range oddQuoteRuns(arg[1 : len(arg)-1]) {
		t.Errorf("an unescaped quote at %d ends the argument early: %s", i, line)
	}
}

// oddQuoteRuns reports the offsets of quote runs of odd length, which are the
// ones that close the string they are in.
func oddQuoteRuns(s string) []int {
	var bad []int
	for i := 0; i < len(s); {
		if s[i] != '\'' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '\'' {
			j++
		}
		if (j-i)%2 == 1 {
			bad = append(bad, i)
		}
		i = j
	}
	return bad
}
