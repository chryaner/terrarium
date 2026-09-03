package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
)

// An SSH session cannot be cancelled from the client, so a command that
// outlives its deadline keeps running in the guest with nothing holding on to
// it. On Windows it runs in session 0, where a dialog nobody can see waits
// forever. The way back to it is to tag the command line before sending it and
// recognise the tag in the guest's process table from a second session.
const (
	markerPrefix = "trr:"
	// markerVar is only somewhere for a cmd guest's marker to sit on the
	// command line. Nothing reads it.
	markerVar = "TRR_MARK"
	// killTimeout bounds the second session. Finding and killing a process is
	// fast, and a kill that hangs would hide the timeout that caused it.
	killTimeout = 60 * time.Second
)

// newMarkerID is the per-command half of the marker. Random rather than the
// env name: two execs against the same env must not kill each other.
func newMarkerID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only the uniqueness of one command's tag rides on this, and a clock
		// reading is unique enough for that.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

// markCommand appends the marker to the command line a session carries, in
// syntax the guest's own shell ignores. shell is the shell that reads the
// line, not the shell the command was written for: the marker has to survive
// that parser to reach the process table at all.
//
// The POSIX wrapper is not decoration. A login shell handed one simple command
// execs it, and its own argv - marker included - is gone; wrapping in `sh -c`
// with a trailing comment leaves a shell holding the marker, and that shell is
// the session's process group leader, so killing its group takes the command
// with it.
func markCommand(shell, command, id string) string {
	marker := markerPrefix + id
	switch shell {
	case ShellCmd:
		// The marker goes first here, and it is a `set` rather than a comment.
		// cmd.exe has no comment inside a command line, and `rem` last would be
		// the command whose status cmd /c returns, so a failing command would
		// come back 0 - which is every MCP exec on a cmd guest, where the marker
		// is unconditional. Nor can rem go first: `rem x & cmd` comments the
		// command out, and `(rem x) & cmd` does not parse at all, because rem
		// swallows the closing paren. `set` is an ordinary command the parser
		// splits on & like any other, so the caller's command stays last and its
		// exit code is what comes back.
		return "set " + markerVar + "=" + marker + " & " + command
	case ShellPowerShell:
		return command + " # " + marker
	default:
		return sshx.QuotePOSIX([]string{"sh", "-c", command + " # " + marker})
	}
}

// killScript builds the script that finds everything carrying the marker and
// kills it, and reports which shell has to read that script. It goes over
// stdin rather than as a command line: it needs pipes and quotes that neither
// cmd.exe nor a PowerShell command line would carry intact, and a cmd guest
// still has powershell.exe to read it with.
//
// Neither script contains the marker as one string. A killer whose own command
// line held it would match itself - and on Windows the shell sshd started for
// it too, which is the session reading the answer.
func killScript(shell, id string) (script, scriptShell string) {
	if shell == ShellPOSIX {
		// TERM first, because a process given the chance to clean up usually
		// takes it, then KILL for one that ignored it - and that is reported,
		// because "killed" has to mean killed. The marked shell leads its
		// process group, so the group kill takes its children with it; the
		// plain kill covers the case where it leads none.
		pat := "'trr'':" + id + "'"
		return "found=$(pgrep -af " + pat + ")\n" +
			"[ -n \"$found\" ] || exit 0\n" +
			"echo \"$found\" | while read -r p rest; do\n" +
			"  kill -s TERM -- \"-$p\" 2>/dev/null || kill -s TERM \"$p\" 2>/dev/null\n" +
			"done\n" +
			"sleep 2\n" +
			"echo \"$found\" | while read -r p rest; do\n" +
			"  if kill -0 \"$p\" 2>/dev/null; then\n" +
			"    kill -s KILL -- \"-$p\" 2>/dev/null || kill -s KILL \"$p\" 2>/dev/null\n" +
			"    echo \"$p $rest (ignored SIGTERM, killed)\"\n" +
			"  else\n" +
			"    echo \"$p $rest\"\n" +
			"  fi\n" +
			"done\n", ShellPOSIX
	}
	// taskkill /T is what makes this a tree kill: the marker is on the shell
	// sshd started, and the process actually hanging is its child.
	//
	// One line, because `powershell -Command -` reads stdin the way a prompt
	// does: a pipeline broken across lines by a trailing | is silently
	// abandoned there, and this would report killing nothing.
	return "$m = 'trr:' + '" + id + "'; " +
		"Get-CimInstance Win32_Process | " +
		"Where-Object { $_.CommandLine -and $_.CommandLine.Contains($m) -and $_.ProcessId -ne $PID } | " +
		"ForEach-Object { taskkill /F /T /PID $_.ProcessId | Out-Null; " +
		"'{0} {1}' -f $_.ProcessId, $_.CommandLine.Trim() }\n", ShellPowerShell
}

// KilledError reports a command that outlived its deadline and was killed for
// it. It replaces sshx.TimeoutError's "probably still running", which is
// exactly what --kill-on-timeout is there to stop being true.
type KilledError struct {
	Timeout time.Duration
	Command string
	// Killed is what the guest reported killing, one process per line. Empty
	// means the marker matched nothing: the command finished in the gap
	// between the deadline and the kill.
	Killed string
}

func (e *KilledError) Error() string {
	if e.Killed == "" {
		return fmt.Sprintf("command did not finish within %s: %s\nnothing left to kill in the guest: it had already exited",
			e.Timeout, e.Command)
	}
	return fmt.Sprintf("command did not finish within %s: %s\nkilled in the guest:\n%s",
		e.Timeout, e.Command, e.Killed)
}

// killMarked opens a second session and kills whatever the marker still names.
// A failed kill keeps the timeout as the reported cause: the command not
// finishing is the thing the caller asked about, and the cleanup failing is
// detail on top of it.
//
// command is what the caller asked to run, not the marked line it was sent as:
// the marker is bookkeeping, and showing it only makes the reader wonder what
// they typed wrong.
//
// The kill gets its own context rather than the caller's. The caller's is
// often already done - a cancelled exec is one of the reasons to be here - and
// a kill that skips itself for that leaves exactly the process it was for.
func (e *Engine) killMarked(envName, shell, id string, timeout time.Duration, command string) error {
	script, scriptShell := killScript(shell, id)
	var out sshx.OutputBuffer
	_, err := e.run(context.Background(), envName, killTimeout,
		ScriptCommand(scriptShell), strings.NewReader(script), &out, &out)
	if err != nil {
		return fmt.Errorf("command did not finish within %s: %s\nkilling it in the guest failed too: %v",
			timeout, command, err)
	}
	return &KilledError{Timeout: timeout, Command: command, Killed: strings.TrimSpace(out.String())}
}
