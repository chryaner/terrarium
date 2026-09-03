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
func hasActiveConsoleSession(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		// The current session is marked with a leading >.
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), ">"))
		if len(fields) == 0 || !strings.EqualFold(fields[0], "console") {
			continue
		}
		for _, f := range fields[1:] {
			if strings.EqualFold(f, "Active") {
				return true
			}
		}
	}
	return false
}

var (
	taskStatusRe = regexp.MustCompile(`(?m)^Status:\s*(.*?)\s*$`)
	taskResultRe = regexp.MustCompile(`(?m)^Last Result:\s*(.*?)\s*$`)
)

// parseTaskQuery reads `schtasks /Query /FO LIST /V`. done reports that the
// command has finished and code is its exit code; until then code means
// nothing. A query that says neither is not an answer, so it is not treated
// as one - polling carries on.
func parseTaskQuery(out string) (done bool, code int) {
	status := taskStatusRe.FindStringSubmatch(out)
	result := taskResultRe.FindStringSubmatch(out)
	if status == nil || result == nil {
		return false, 0
	}
	if strings.EqualFold(status[1], "Running") {
		return false, 0
	}
	n, err := strconv.Atoi(result[1])
	if err != nil {
		return false, 0
	}
	if n == schedTaskRunning || n == schedTaskHasNotRun {
		return false, 0
	}
	return true, n
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
func desktopTaskAction(command, outFile, id string) string {
	return MarkCommand(ShellCmd, "cmd /c "+command+" > "+outFile+" 2>&1", id)
}

// desktopCreateScript registers the task and starts it. /IT puts it in the
// interactive session, /RL HIGHEST keeps the elevation an SSH session already
// had, and /SC ONCE with a start time in the past never fires by itself -
// schtasks /Run is what starts it.
func desktopCreateScript(task, action string) string {
	return "schtasks /Create /TN " + psQuote(task) + " /TR " + psQuote(action) +
		" /SC ONCE /ST 00:00 /IT /RL HIGHEST /F\n" +
		"schtasks /Run /TN " + psQuote(task) + "\n"
}

func desktopQueryScript(task string) string {
	return "schtasks /Query /TN " + psQuote(task) + " /FO LIST /V\n"
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

// noConsoleSessionErr says what to do rather than only what is wrong: the
// machines this happens on are goldens built before the post-install set up
// automatic logon, and logging in by hand is a three-command fix.
func noConsoleSessionErr(env string) error {
	return fmt.Errorf("no user is logged on at the console of %q, so --desktop has no session to run in.\n"+
		"log one in first: `terrarium screenshot %s` to see the logon screen, `terrarium type %s <password>`, `terrarium keys %s enter`.\n"+
		"goldens built from now on log on by themselves; one built before that has to be logged in once per boot",
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
	probe, _, err := e.runScript(ctx, r.Env, ShellPowerShell, "query session\n")
	if err != nil {
		return -1, err
	}
	if !hasActiveConsoleSession(probe) {
		return -1, noConsoleSessionErr(r.Env)
	}

	id := NewMarkerID()
	task := "trr-" + id
	outFile := desktopOutDir + `\` + task + ".out"
	action := desktopTaskAction(r.Command, outFile, id)
	if out, _, err := e.runScript(ctx, r.Env, ShellPowerShell, desktopCreateScript(task, action)); err != nil {
		return -1, fmt.Errorf("registering the desktop task failed: %w\n%s", err, strings.TrimSpace(out))
	}

	code, err := e.waitDesktopTask(ctx, r, task)
	if err != nil {
		return -1, err
	}
	// Read before cleaning up: the file is the command's whole output.
	out, _, readErr := e.runScript(ctx, r.Env, ShellPowerShell, desktopReadScript(outFile))
	e.runScript(ctx, r.Env, ShellPowerShell, desktopCleanupScript(task, outFile))
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
		out, _, err := e.runScript(ctx, r.Env, ShellPowerShell, desktopQueryScript(task))
		if err != nil {
			return -1, err
		}
		if done, code := parseTaskQuery(out); done {
			return code, nil
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
	outFile := desktopOutDir + `\` + task + ".out"
	if !r.KillOnTimeout {
		return fmt.Errorf("command did not finish within %s and is still running on the guest's desktop: %s\n"+
			"look at it with `terrarium screenshot %s`, stop it with `terrarium exec %s -- schtasks /End /TN %s`",
			r.Timeout, r.Command, r.Env, r.Env, task)
	}
	ctx := context.Background()
	// The marker kill comes first, and it is the one that matters: /End stops
	// the task's own process and leaves whatever that process started running,
	// which is exactly the window the caller was waiting on. Killing the
	// marked tree takes both; /End afterwards clears the task's running state.
	id := strings.TrimPrefix(task, "trr-")
	err := e.killMarked(r.Env, ShellCmd, id, r.Timeout, r.Command)
	e.runScript(ctx, r.Env, ShellPowerShell, desktopEndScript(task))
	e.runScript(ctx, r.Env, ShellPowerShell, desktopCleanupScript(task, outFile))
	return err
}
