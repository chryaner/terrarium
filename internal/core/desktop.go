package core

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
)

// A command sent over SSH to a Windows guest runs in session 0, the service
// session: it has no desktop, so anything that opens a window or a dialog
// waits for a click nobody can make and nothing can see. --desktop hands the
// command to the task scheduler marked /IT instead, which runs it in the
// session a user is actually logged into - the one a screenshot shows.
const (
	// desktopPollInterval is how often the task's status is read. Each poll is
	// its own session, so this trades round trips against how quickly a short
	// command's output comes back.
	desktopPollInterval = time.Second
	// desktopStepTimeout bounds one bookkeeping command (create, query, read,
	// clean up), as opposed to the command being run.
	desktopStepTimeout = 60 * time.Second
	// desktopOutDir is readable and writable from both sessions: the task
	// writes there as the logged-in user, exec reads it back over SSH.
	desktopOutDir = `C:\Windows\Temp`
)

// Task status codes schtasks reports in Last Result while there is no exit
// code to report yet. Without them the first poll - which can land before the
// task has started - reads "has not yet run" as the command's exit code.
const (
	schedTaskRunning   = 267009 // 0x41301
	schedTaskHasNotRun = 267011 // 0x41303
)

// hasActiveConsoleSession reads `query session` output. Only the console
// session counts: an /IT task runs where the user is interactively logged in,
// and a disconnected session has no screen behind it for a screenshot to show.
//
// read is false when there was no console line at all, which is what a
// localised or changed output looks like from here. The STATE word itself is
// localised on a non-English Windows, so a console line that is logged on can
// still read as logged out - the error that follows says how to log one in,
// which is the right thing to try either way.
func hasActiveConsoleSession(out string) (active, read bool) {
	for _, line := range strings.Split(out, "\n") {
		// The current session is marked with a leading >.
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), ">"))
		if len(fields) == 0 || !strings.EqualFold(fields[0], "console") {
			continue
		}
		read = true
		for _, f := range fields[1:] {
			if strings.EqualFold(f, "Active") {
				return true, true
			}
		}
	}
	return false, read
}

var (
	taskStatusRe = regexp.MustCompile(`(?m)^Status:\s*(.*?)\s*$`)
	taskResultRe = regexp.MustCompile(`(?m)^Last Result:\s*(.*?)\s*$`)
)

// parseTaskQuery reads `schtasks /Query /FO LIST /V`. done reports that the
// command has finished and code is its exit code; until then code means
// nothing. read is false when neither field was there - a localised Windows,
// or a task that is gone - and the caller says so rather than polling until
// the timeout and then claiming the command is still running.
func parseTaskQuery(out string) (done bool, code int, read bool) {
	status := taskStatusRe.FindStringSubmatch(out)
	result := taskResultRe.FindStringSubmatch(out)
	if status == nil || result == nil {
		return false, 0, false
	}
	if strings.EqualFold(status[1], "Running") {
		return false, 0, true
	}
	n, err := strconv.Atoi(result[1])
	if err != nil {
		return false, 0, false
	}
	if n == schedTaskRunning || n == schedTaskHasNotRun {
		return false, 0, true
	}
	return true, n, true
}

// unreadableErr reports guest output terrarium cannot make sense of, with
// enough of it to see why. Everything --desktop reads is an English Windows
// command's output; on any other one this is the honest answer.
func unreadableErr(what, out string) error {
	out = strings.TrimSpace(out)
	if len(out) > 800 {
		out = out[len(out)-800:]
	}
	return fmt.Errorf("could not read the guest's %s output - a non-English Windows reports these fields in its own language, and --desktop cannot drive it:\n%s", what, out)
}

// psQuote spells one string as a PowerShell single-quoted literal, where
// nothing at all expands and a quote is written twice.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// desktopTaskAction is what the scheduled task runs: the command under
// cmd.exe, with both its streams captured to a file exec can read back, and
// the marker that makes it killable. cmd.exe rather than the guest's own
// shell because a task action is a command line, not a session.
//
// The command is parenthesised because a redirect binds to one command:
// `a & b > f` sends only b's output to the file, and `a | b > f` only b's,
// so anything compound would lose most of what it printed. A block redirects
// as a whole, and still exits with what its last command exited with.
func desktopTaskAction(command, outFile, id string) string {
	return "cmd /c " + MarkCommand(ShellCmd, "( "+command+" ) > "+outFile+" 2>&1", id)
}

// exitWithLastCode is what makes a schtasks failure visible. PowerShell exits
// 0 after running a script whatever the native commands in it did, so without
// this a refused /Create looks like a task that was registered and the caller
// waits out the whole timeout for a command that never ran.
const exitWithLastCode = "exit $LASTEXITCODE\n"

// desktopCreateScript registers the task and starts it. /IT puts it in the
// interactive session, /RL HIGHEST keeps the elevation an SSH session already
// had, and /SC ONCE with a start time in the past never fires by itself -
// schtasks /Run is what starts it.
func desktopCreateScript(task, action string) string {
	return "schtasks /Create /TN " + psQuote(task) + " /TR " + psQuote(action) +
		" /SC ONCE /ST 00:00 /IT /RL HIGHEST /F\n" +
		"schtasks /Run /TN " + psQuote(task) + "\n" +
		exitWithLastCode
}

func desktopQueryScript(task string) string {
	return "schtasks /Query /TN " + psQuote(task) + " /FO LIST /V\n" + exitWithLastCode
}

func desktopEndScript(task string) string {
	return "schtasks /End /TN " + psQuote(task) + "\n"
}

func desktopReadScript(outFile string) string {
	return "Get-Content -Raw " + psQuote(outFile) + " -ErrorAction SilentlyContinue\n"
}

func desktopCleanupScript(task, outFile string) string {
	return "schtasks /Delete /TN " + psQuote(task) + " /F | Out-Null\n" +
		"Remove-Item " + psQuote(outFile) + " -Force -ErrorAction SilentlyContinue\n"
}

// noConsoleSessionErr says what to do rather than only what is wrong: this
// happens on goldens whose post-install did not set up automatic logon, and
// logging in by hand is a three-command fix.
func noConsoleSessionErr(env string) error {
	return fmt.Errorf("no user is logged on at the console of %q, so --desktop has no session to run in.\n"+
		"log one in first: `terrarium screenshot %s` to see the logon screen, `terrarium type %s <password>`, `terrarium keys %s enter`.\n"+
		"a golden built with automatic logon logs itself on; one without has to be logged in once per boot",
		env, env, env, env)
}

// runScript runs a PowerShell (or sh) script in the guest over the normal
// session and returns what it printed. Everything --desktop does apart from
// the command itself is bookkeeping in the guest, and all of it goes over
// stdin so no shell on the way re-parses it.
func (e *Engine) runScript(ctx context.Context, envName, shell, script string) (string, int, error) {
	var out sshx.OutputBuffer
	code, err := e.Run(ctx, envName, desktopStepTimeout, ScriptCommand(shell), strings.NewReader(script), &out, &out)
	return out.String(), code, err
}

// execDesktop runs a command in the guest's interactive session and waits for
// it, so a command that opens a window opens it where a screenshot can see it.
func (e *Engine) execDesktop(ctx context.Context, r ExecRequest) (int, error) {
	if r.GuestShell == ShellPOSIX {
		return -1, fmt.Errorf("--desktop is for Windows guests: a POSIX guest's exec already runs in a real session, and there is no service session to escape from")
	}
	if r.Stdin != nil {
		return -1, fmt.Errorf("--desktop takes a command, not a script: a scheduled task is a command line with no stdin to read one from")
	}
	// Every step below is a PowerShell script over stdin: a cmd guest has
	// powershell.exe too, and nothing else quotes schtasks' arguments safely.
	//
	// query session's own exit code is 1 even when it worked, so only its
	// output is read.
	probe, _, err := e.runScript(ctx, r.Env, ShellPowerShell, "query session\n")
	if err != nil {
		return -1, err
	}
	active, read := hasActiveConsoleSession(probe)
	if !read {
		return -1, unreadableErr("query session", probe)
	}
	if !active {
		return -1, noConsoleSessionErr(r.Env)
	}

	id := NewMarkerID()
	task := "trr-" + id
	outFile := desktopOutDir + `\` + task + ".out"
	action := desktopTaskAction(r.Command, outFile, id)
	out, code, err := e.runScript(ctx, r.Env, ShellPowerShell, desktopCreateScript(task, action))
	if err != nil {
		return -1, fmt.Errorf("registering the desktop task failed: %w\n%s", err, strings.TrimSpace(out))
	}
	if code != 0 {
		// schtasks refuses an action longer than it will store, a task name
		// that clashes, a machine with no task scheduler running. Silently,
		// as far as PowerShell is concerned.
		return -1, fmt.Errorf("schtasks exited %d registering the desktop task, so the command never ran:\n%s",
			code, strings.TrimSpace(out))
	}
	// The task exists from here, so it goes whatever happens next, the paths
	// that return an error included. Best effort: a cleanup that fails leaves
	// a dead task and a scratch file, which is not worth losing the command's
	// own result over.
	defer e.runScript(context.Background(), r.Env, ShellPowerShell, desktopCleanupScript(task, outFile))

	code, err = e.waitDesktopTask(ctx, r, task)
	if err != nil {
		return -1, err
	}
	// Read before the deferred cleanup: the file is the command's whole output.
	out, _, readErr := e.runScript(ctx, r.Env, ShellPowerShell, desktopReadScript(outFile))
	if readErr != nil {
		return code, readErr
	}
	if r.Stdout != nil {
		fmt.Fprint(r.Stdout, out)
	}
	return code, nil
}

// waitDesktopTask polls until the task stops running, and handles the deadline
// passing while it still is.
func (e *Engine) waitDesktopTask(ctx context.Context, r ExecRequest, task string) (int, error) {
	deadline := time.Now().Add(r.Timeout)
	for {
		out, code, err := e.runScript(ctx, r.Env, ShellPowerShell, desktopQueryScript(task))
		if err != nil {
			return -1, err
		}
		if code != 0 {
			return -1, fmt.Errorf("schtasks exited %d reading the desktop task's status:\n%s",
				code, strings.TrimSpace(out))
		}
		done, taskCode, read := parseTaskQuery(out)
		if !read {
			return -1, unreadableErr("schtasks /Query", out)
		}
		if done {
			return taskCode, nil
		}
		if !time.Now().Before(deadline) {
			return -1, e.desktopTimeout(r, task)
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(desktopPollInterval):
		}
	}
}

// desktopTimeout stops the task if the caller asked for that, and otherwise
// says how to stop it by hand: the command is still on the guest's screen,
// which is sometimes exactly what the caller wants to look at.
func (e *Engine) desktopTimeout(r ExecRequest, task string) error {
	if !r.KillOnTimeout {
		return fmt.Errorf("command did not finish within %s and is still running on the guest's desktop: %s\n"+
			"look at it with `terrarium screenshot %s`; --kill-on-timeout would have killed it and its children",
			r.Timeout, r.Command, r.Env)
	}
	// The marker kill comes first, and it is the one that matters: /End stops
	// the task's own process and leaves whatever that process started running,
	// which is exactly the window the caller was waiting on. Killing the
	// marked tree takes both; /End afterwards only clears the task's running
	// state, so its failure has nothing to add to the kill's own result.
	id := strings.TrimPrefix(task, "trr-")
	err := e.killMarked(r.Env, ShellCmd, id, r.Timeout, r.Command)
	e.runScript(context.Background(), r.Env, ShellPowerShell, desktopEndScript(task))
	return err
}
