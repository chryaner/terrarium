package core

import (
	"strings"
	"testing"
)

// Only an Active console session has a screen behind it. Getting this wrong
// either refuses a guest that could have run the command, or registers a task
// that never starts and times out with nothing to show.
func TestHasActiveConsoleSession(t *testing.T) {
	loggedIn := " SESSIONNAME       USERNAME                 ID  STATE   TYPE        DEVICE \r\n" +
		">services                                    0  Disc                        \r\n" +
		" console           terrarium                 1  Active                      \r\n"
	loggedOut := " SESSIONNAME       USERNAME                 ID  STATE   TYPE        DEVICE \r\n" +
		">services                                    0  Disc                        \r\n" +
		" console                                     1  Conn                        \r\n"
	rdpOnly := " SESSIONNAME       USERNAME                 ID  STATE   TYPE        DEVICE \r\n" +
		" rdp-tcp#1         terrarium                 2  Active  rdpwd              \r\n"
	for _, c := range []struct {
		name     string
		out      string
		want     bool
		wantRead bool
	}{
		{"a user at the console", loggedIn, true, true},
		{"the logon screen", loggedOut, false, true},
		// No console line at all: this output says nothing either way, and
		// saying so beats reporting a machine as logged out.
		{"only a remote desktop session", rdpOnly, false, false},
		{"nothing at all", "", false, false},
	} {
		got, read := hasActiveConsoleSession(c.out)
		if got != c.want || read != c.wantRead {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", c.name, got, read, c.want, c.wantRead)
		}
	}
}

// The status a task reports right after being registered is not an exit code,
// and reading it as one would report a command as finished before it started.
func TestParseTaskQuery(t *testing.T) {
	query := func(status, result string) string {
		return "Folder: \\\r\nHostName:      WIN\r\nTaskName:      \\trr-1\r\n" +
			"Status:                               " + status + "\r\n" +
			"Logon Mode:                           Interactive only\r\n" +
			"Last Result:                          " + result + "\r\n"
	}
	for _, c := range []struct {
		name     string
		out      string
		wantDone bool
		wantCode int
		wantRead bool
	}{
		{"finished cleanly", query("Ready", "0"), true, 0, true},
		{"finished with a failure", query("Ready", "3"), true, 3, true},
		{"still running", query("Running", "267009"), false, 0, true},
		{"registered but not started yet", query("Ready", "267011"), false, 0, true},
		// Reported, rather than polled until the timeout and then blamed on
		// the command: a localised Windows names these fields differently.
		{"an answer with neither field is not an answer", "ERROR: task not found", false, 0, false},
	} {
		done, code, read := parseTaskQuery(c.out)
		if done != c.wantDone || (done && code != c.wantCode) || read != c.wantRead {
			t.Errorf("%s: got done=%v code=%d read=%v, want done=%v code=%d read=%v",
				c.name, done, code, read, c.wantDone, c.wantCode, c.wantRead)
		}
	}
}

// The task action is the only part of --desktop that runs the caller's
// command, so it has to redirect both streams to somewhere readable, carry the
// marker that makes a timeout killable, and still exit with what the command
// exited with.
func TestDesktopTaskAction(t *testing.T) {
	got := desktopTaskAction("notepad", `C:\Windows\Temp\trr-1.out`, "abc123")
	want := `cmd /c set TRR_MARK=trr:abc123 & ( notepad ) > C:\Windows\Temp\trr-1.out 2>&1`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// A redirect binds to one command, so a compound command has to be
	// wrapped: `a & b > f` sends only b's output to the file, and the file is
	// all the caller gets back.
	compound := desktopTaskAction("echo a & dir /b x", "out", "abc123")
	if !strings.Contains(compound, "( echo a & dir /b x ) > out") {
		t.Errorf("a compound command is not parenthesised: %s", compound)
	}
	// cmd /c reports what its last command exited with, so nothing of ours
	// may run after the caller's.
	if !strings.HasSuffix(compound, "2>&1") {
		t.Errorf("something runs after the command, so its exit code is lost: %s", compound)
	}
}

// schtasks takes its arguments from PowerShell, which is the one shell that
// can quote them: a command with a quote in it must not end the string it is
// pasted into.
func TestDesktopScriptsQuote(t *testing.T) {
	script := desktopCreateScript("trr-1", `cmd /c echo it's > out`)
	if !strings.Contains(script, `'cmd /c echo it''s > out'`) {
		t.Errorf("the task action is not escaped for PowerShell:\n%s", script)
	}
	for _, want := range []string{"/IT", "/RL HIGHEST", "/SC ONCE", "/F", "schtasks /Run"} {
		if !strings.Contains(script, want) {
			t.Errorf("create script is missing %q:\n%s", want, script)
		}
	}
	// A pipeline broken across lines is silently abandoned by
	// `powershell -Command -`, so no line here may end in one.
	for _, line := range strings.Split(desktopCleanupScript("trr-1", "out"), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "|") {
			t.Errorf("a line ends in a pipe, which PowerShell reading stdin drops: %q", line)
		}
	}
}

// PowerShell exits 0 whatever the native commands in a script did, so without
// this a refused schtasks looks like a task that was registered and the caller
// waits out the whole timeout for a command that never ran.
func TestDesktopScriptsReportSchtasksFailure(t *testing.T) {
	for name, script := range map[string]string{
		"create": desktopCreateScript("trr-1", "cmd /c echo x"),
		"query":  desktopQueryScript("trr-1"),
	} {
		if !strings.Contains(script, "exit $LASTEXITCODE") {
			t.Errorf("the %s script swallows schtasks' exit code:\n%s", name, script)
		}
	}
}

// The error a user hits on an older golden is the whole feature for them, so
// it has to name the way out rather than only the problem.
func TestNoConsoleSessionErrSaysWhatToDo(t *testing.T) {
	msg := noConsoleSessionErr("w1").Error()
	for _, want := range []string{"screenshot w1", "type w1", "keys w1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not say to %q: %s", want, msg)
		}
	}
}

// Output terrarium cannot parse has to be shown, not guessed at: the reader
// needs to see that it was a language problem rather than a broken command.
func TestUnreadableErrShowsTheOutput(t *testing.T) {
	msg := unreadableErr("schtasks /Query", "Status:                Wird ausgefuehrt").Error()
	for _, want := range []string{"schtasks /Query", "Wird ausgefuehrt", "non-English"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q: %s", want, msg)
		}
	}
}
