package mcpserver

import (
	"errors"
	"io"
	"testing"

	"github.com/chryaner/terrarium/internal/core"
)

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	if r == nil {
		return ""
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// env_exec promises a plain command reaches the guest's own shell as one
// string, so an agent's pipes and redirects work. The shell and script
// parameters are the way out of that promise, not an erosion of it.
func TestExecRequest(t *testing.T) {
	guest := func(shell string) func() (string, error) {
		return func() (string, error) { return shell, nil }
	}
	cases := []struct {
		name    string
		in      envExecInput
		shell   string
		command string
		stdin   string
	}{
		{
			"a plain command is not touched",
			envExecInput{Command: "ls -la | wc -l"}, core.ShellPOSIX,
			"ls -la | wc -l", "",
		},
		{
			"a script goes to the guest's own shell on stdin",
			envExecInput{Script: "set -e\nmake\n"}, core.ShellPOSIX,
			"sh -s", "set -e\nmake\n",
		},
		{
			"a script on a Windows guest goes to PowerShell",
			envExecInput{Script: "$a = 1\nWrite-Output $a\n"}, core.ShellCmd,
			"powershell -NoProfile -NonInteractive -Command -", "$a = 1\nWrite-Output $a\n",
		},
		{
			// The point of the shell parameter: a cmd guest can still be
			// handed PowerShell, and the text never meets cmd's parser.
			"a command under a forced shell also goes over stdin",
			envExecInput{Command: "Get-Process | Select -First 1", Shell: "powershell"}, core.ShellCmd,
			"powershell -NoProfile -NonInteractive -Command -", "Get-Process | Select -First 1",
		},
		{
			"sh is spelled the way the parameter documents it",
			envExecInput{Command: "id", Shell: "sh"}, core.ShellCmd,
			"sh -s", "id",
		},
		{
			// The wrapper is quoted for the guest's own shell, which parses
			// the line before cmd.exe is started at all.
			"cmd has no stdin mode, so its command line is wrapped instead",
			envExecInput{Command: "dir /b", Shell: "cmd"}, core.ShellPowerShell,
			"cmd /c 'dir /b'", "",
		},
		{
			"a cmd command is not left for PowerShell to expand",
			envExecInput{Command: "echo $env:USERNAME & whoami", Shell: "cmd"}, core.ShellPowerShell,
			"cmd /c 'echo $env:USERNAME & whoami'", "",
		},
		{
			"a cmd guest reads the cmd line itself, so it goes over as typed",
			envExecInput{Command: "dir /b", Shell: "cmd"}, core.ShellCmd,
			"cmd /c dir /b", "",
		},
	}
	for _, c := range cases {
		cmd, stdin, err := execRequest(c.in, guest(c.shell))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if cmd != c.command {
			t.Errorf("%s: command got %q, want %q", c.name, cmd, c.command)
		}
		if got := readAll(t, stdin); got != c.stdin {
			t.Errorf("%s: stdin got %q, want %q", c.name, got, c.stdin)
		}
	}
}

func TestExecRequestRejections(t *testing.T) {
	guest := func() (string, error) { return core.ShellPOSIX, nil }
	for _, c := range []struct {
		name string
		in   envExecInput
	}{
		{"neither command nor script says nothing about what to run", envExecInput{}},
		{"both would have to pick one silently", envExecInput{Command: "id", Script: "id"}},
		{"cmd cannot read a script from stdin", envExecInput{Script: "dir\n", Shell: "cmd"}},
		{"a shell terrarium cannot quote for", envExecInput{Command: "id", Shell: "bash"}},
	} {
		if _, _, err := execRequest(c.in, guest); err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
	}
	// A guest that cannot be asked is a failure, not a default.
	failing := func() (string, error) { return "", errors.New("vm is off") }
	if _, _, err := execRequest(envExecInput{Script: "id\n"}, failing); err == nil {
		t.Error("an unreachable guest should fail rather than guess a shell")
	}
	// Same for the cmd wrapper: which shell carries the line decides how that
	// line has to be quoted, so an unknown one cannot be guessed at either.
	if _, _, err := execRequest(envExecInput{Command: "dir", Shell: "cmd"}, failing); err == nil {
		t.Error("an unreachable guest should fail rather than guess how to quote")
	}
}
