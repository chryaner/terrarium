package core

import (
	"strings"
	"testing"
	"time"
)

// The marker has to reach the guest's process table, which means surviving
// the shell that parses the command line on the way. Each shell ignores a
// different thing, and getting it wrong either breaks the command or leaves
// nothing to find afterwards.
func TestMarkCommand(t *testing.T) {
	for _, c := range []struct {
		name    string
		shell   string
		command string
		want    string
	}{
		{
			"PowerShell ignores the rest of the line after a hash",
			ShellPowerShell, "Start-Sleep 300", "Start-Sleep 300 # trr:abc123",
		},
		{
			// The marker goes first: last, it would be the command whose exit
			// code cmd /c hands back.
			"cmd marks in front, with a command the parser splits on",
			ShellCmd, "notepad", "set TRR_MARK=trr:abc123 & notepad",
		},
		{
			// Bare, a login shell execs the command and the marker goes with
			// its argv; the wrapper leaves a shell holding it.
			"a POSIX command is wrapped so a shell keeps the marker",
			ShellPOSIX, "sleep 300", `sh -c 'sleep 300 # trr:abc123'`,
		},
		{
			"a POSIX command's own quoting is quoted again by the wrapper",
			ShellPOSIX, `grep 'a|b' f`, `sh -c 'grep '\''a|b'\'' f # trr:abc123'`,
		},
	} {
		if got := markCommand(c.shell, c.command, "abc123"); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestNewMarkerIDIsUnique(t *testing.T) {
	// Two commands in one env must not kill each other, which is the whole
	// reason the id is not the env name.
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newMarkerID()
		if seen[id] {
			t.Fatalf("marker id %q handed out twice", id)
		}
		seen[id] = true
	}
}

// The killer runs in the guest too, so its own command line must not contain
// the thing it searches for: it would match itself and the shell that carries
// it, and kill the session reading the answer.
func TestKillScriptDoesNotMatchItself(t *testing.T) {
	for _, shell := range []string{ShellPOSIX, ShellCmd, ShellPowerShell} {
		script, scriptShell := killScript(shell, "abc123")
		if strings.Contains(script, "trr:abc123") {
			t.Errorf("%s: the kill script spells the marker out: %s", shell, script)
		}
		if !strings.Contains(script, "abc123") {
			t.Errorf("%s: the kill script does not carry the id at all", shell)
		}
		want := ShellPowerShell
		if shell == ShellPOSIX {
			want = ShellPOSIX
		}
		if scriptShell != want {
			t.Errorf("%s: script shell got %q, want %q", shell, scriptShell, want)
		}
	}
}

// The two kills have to be tree kills: the marker is on the shell sshd
// started, and the process actually hanging is its child.
func TestKillScriptKillsTheTree(t *testing.T) {
	win, _ := killScript(ShellCmd, "abc123")
	if !strings.Contains(win, "taskkill /F /T /PID") {
		t.Errorf("Windows kill is not a tree kill:\n%s", win)
	}
	if !strings.Contains(win, "$_.ProcessId -ne $PID") {
		t.Errorf("Windows kill does not exclude its own process:\n%s", win)
	}
	posix, _ := killScript(ShellPOSIX, "abc123")
	if !strings.Contains(posix, `kill -s TERM -- "-$p"`) {
		t.Errorf("POSIX kill does not kill the process group:\n%s", posix)
	}
}

// The error a killed command reports is the one thing the caller sees, so it
// has to say the command was killed rather than left running, and name what
// went.
func TestKilledError(t *testing.T) {
	e := &KilledError{Timeout: 5 * time.Second, Command: "notepad & rem trr:abc123", Killed: "4321 cmd.exe /c notepad"}
	msg := e.Error()
	for _, want := range []string{"5s", "notepad", "killed in the guest", "4321"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error text is missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "still running") {
		t.Errorf("a killed command must not be reported as still running: %s", msg)
	}
	// The race the marker cannot win: the command finished between the
	// deadline and the kill. Saying nothing was killed is the honest answer.
	empty := (&KilledError{Timeout: time.Second, Command: "x"}).Error()
	if !strings.Contains(empty, "already exited") {
		t.Errorf("an empty kill should say so: %s", empty)
	}
}

// A marker that changes what a command exits with is worse than no marker:
// MCP marks every command on a cmd guest, and --desktop marks every task, so
// a failing command would silently report success. Verified against cmd.exe:
// `cmd /c "dir /b nosuchfile & rem x"` exits 0, and the same line without the
// marker exits 1.
func TestMarkCommandLeavesTheCommandLast(t *testing.T) {
	for _, shell := range []string{ShellCmd, ShellPowerShell, ShellPOSIX} {
		marked := markCommand(shell, "failing-command", "abc123")
		switch shell {
		case ShellCmd:
			// Nothing may run after the command, because cmd /c reports what
			// the last one exited with.
			if !strings.HasSuffix(marked, "failing-command") {
				t.Errorf("%s: the command is not last, so its exit code is lost: %s", shell, marked)
			}
			if strings.Contains(marked, "rem") {
				t.Errorf("%s: rem cannot carry this marker - last it eats the exit code, first it eats the command: %s", shell, marked)
			}
		default:
			// A comment is not a command, so these two keep it at the end.
			if !strings.Contains(marked, "failing-command") {
				t.Errorf("%s: the command went missing: %s", shell, marked)
			}
		}
		if !strings.Contains(marked, "trr:abc123") {
			t.Errorf("%s: the marker is not on the command line: %s", shell, marked)
		}
	}
}

// A process that ignores SIGTERM is still running, and reporting it as killed
// is the one thing kill-on-timeout must not do.
func TestPOSIXKillEscalates(t *testing.T) {
	script, _ := killScript(ShellPOSIX, "abc123")
	if !strings.Contains(script, "kill -s TERM") || !strings.Contains(script, "kill -s KILL") {
		t.Errorf("the POSIX kill does not escalate past TERM:\n%s", script)
	}
	if !strings.Contains(script, "ignored SIGTERM") {
		t.Errorf("a process that needed SIGKILL is not reported as such:\n%s", script)
	}
	if strings.Index(script, "kill -s TERM") > strings.Index(script, "kill -s KILL") {
		t.Errorf("KILL comes before TERM, so nothing gets the chance to clean up:\n%s", script)
	}
}
