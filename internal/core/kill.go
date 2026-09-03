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
	// killTimeout bounds the second session. Finding and killing a process is
	// fast, and a kill that hangs would hide the timeout that caused it.
	killTimeout = 60 * time.Second
)

// NewMarkerID is the per-command half of the marker. Random rather than the
// env name: two execs against the same env must not kill each other.
func NewMarkerID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only the uniqueness of one command's tag rides on this, and a clock
		// reading is unique enough for that.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}

// MarkCommand appends the marker to the command line a session carries, in
// syntax the guest's own shell ignores. shell is the shell that reads the
// line, not the shell the command was written for: the marker has to survive
// that parser to reach the process table at all.
//
// The POSIX wrapper is not decoration. A login shell handed one simple command
// execs it, and its own argv - marker included - is gone; wrapping in `sh -c`
// with a trailing comment leaves a shell holding the marker, and that shell is
// the session's process group leader, so killing its group takes the command
// with it.
func MarkCommand(shell, command, id string) string {
	marker := markerPrefix + id
	switch shell {
	case ShellCmd:
		// cmd.exe has no comment inside a command line. `rem` is a command, so
		// it needs a separator, and & runs it whatever came before exited.
		return command + " & rem " + marker
	case ShellPowerShell:
		return command + " # " + marker
	default:
		return sshx.QuotePOSIX([]string{"sh", "-c", command + " # " + marker})
	}
}

// KillScript builds the script that finds everything carrying the marker and
// kills it, and reports which shell has to read that script. It goes over
// stdin rather than as a command line: it needs pipes and quotes that neither
// cmd.exe nor a PowerShell command line would carry intact, and a cmd guest
// still has powershell.exe to read it with.
//
// Neither script contains the marker as one string. A killer whose own command
// line held it would match itself - and on Windows the shell sshd started for
// it too, which is the session reading the answer.
func KillScript(shell, id string) (script, scriptShell string) {
	if shell == ShellPOSIX {
		// The marked shell leads its process group, so the group kill takes
		// its children; the plain kill is for the case where it does not.
		return "pgrep -af 'trr'':" + id + "' | while read -r p rest; do\n" +
			"  kill -s TERM -- \"-$p\" 2>/dev/null || kill -s TERM \"$p\" 2>/dev/null\n" +
			"  echo \"$p $rest\"\n" +
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
func (e *Engine) killMarked(envName, shell, id string, timeout time.Duration, command string) error {
	script, scriptShell := KillScript(shell, id)
	var out sshx.OutputBuffer
	_, err := e.Run(context.Background(), envName, killTimeout,
		ScriptCommand(scriptShell), strings.NewReader(script), &out, &out)
	if err != nil {
		return fmt.Errorf("command did not finish within %s: %s\nkilling it in the guest failed too: %v",
			timeout, command, err)
	}
	return &KilledError{Timeout: timeout, Command: command, Killed: strings.TrimSpace(out.String())}
}
