package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/spf13/cobra"
)

var (
	execTimeout time.Duration
	execShell   string
	execScript  bool
	execKill    bool
	execDesk    bool
)

var execCmd = &cobra.Command{
	Use:   "exec <env> -- <command...>",
	Short: "Run a command inside an env over SSH",
	Long: `Runs a command in the guest over SSH. The arguments after -- reach the guest
as given, so shell syntax has to be asked for rather than assumed:

  terrarium exec t1 -- sed -e 's/a|b/c/' file    # | stays part of the argument
  terrarium exec t1 -- bash -c 'make && ls *.o'  # pipes, globs, redirects

A leading VAR=value word is the exception the guest shell still reads as its
own: exec t1 -- FOO=bar cmd runs cmd with FOO set, it does not pass FOO=bar to
a command called cmd.

The quoting follows the shell the guest's sshd hands the command to. That is
/bin/sh on Linux, PowerShell on a Windows golden terrarium built, and cmd.exe
on an older or adopted one - cmd has no quoting that rebuilds an argv, so
there its words go over as typed.

  --shell {powershell,cmd,sh}  run under this shell instead
  --stdin                      read a whole script from stdin and run that
  --kill-on-timeout            kill the command in the guest when it times out
  --desktop                    run it in the logged-in session (Windows guests)

--stdin takes no -- command: the script crosses as the shell's own stdin, so
nothing in it is quoted, escaped or split on the way.

  terrarium exec t1 --stdin < setup.ps1

A timeout on its own only stops waiting: the SSH session cannot be cancelled
from here, so the command keeps running in the guest, invisibly. On Windows it
runs in session 0, where anything that opens a dialog waits for a click nobody
can make. --kill-on-timeout tags the command, finds the tag in the guest's
process table from a second session and kills the whole tree, then reports
what it killed.

--desktop is the other half of that. A Windows guest runs an SSH command in
session 0, which has no desktop, so a GUI program or a dialog waits there
unseen. --desktop runs the command as an interactive scheduled task in the
session someone is logged into, so terrarium screenshot shows what it is
waiting on. It needs a user logged on at the console, and says so if there is
none.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if execScript {
			if len(args) != 1 || cmd.ArgsLenAtDash() != -1 {
				return fmt.Errorf("usage: terrarium exec <env> --stdin < script")
			}
			// A script reaches its shell on stdin, and cmd.exe has no way to
			// read one there. Saying so beats quietly running it elsewhere.
			if execShell == "cmd" {
				return fmt.Errorf("--stdin cannot use cmd: it has no script mode, use --shell powershell")
			}
		} else if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
			return fmt.Errorf("usage: terrarium exec <env> -- <command...>")
		}
		want, err := shellFlag(execShell)
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		// What sshd will hand the command line to, whatever shell is wanted:
		// it parses that line before the wanted shell ever sees it. Recorded
		// on the golden, so this is a lookup rather than a round trip once a
		// golden has been reached one time.
		have, err := e.ShellFor(args[0])
		if err != nil {
			return err
		}
		if want == "" {
			want = have
		}

		var stdin io.Reader
		var command string
		if execScript {
			script, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			stdin, command = bytes.NewReader(script), core.ScriptCommand(want)
		} else {
			// A --desktop command is a task action, which cmd.exe runs: the
			// guest's own session shell never sees it.
			carrier := have
			if execDesk {
				carrier = core.ShellCmd
			}
			command = execCommand(want, carrier, args[1:])
		}
		code, err := e.Exec(context.Background(), core.ExecRequest{
			Env:           args[0],
			Command:       command,
			Stdin:         stdin,
			GuestShell:    have,
			Timeout:       execTimeout,
			KillOnTimeout: execKill,
			Desktop:       execDesk,
			Stdout:        os.Stdout,
			Stderr:        os.Stderr,
		})
		if err != nil {
			return err
		}
		if code != 0 {
			os.Exit(code) // propagate the remote exit code
		}
		return nil
	},
}

// shellFlag maps what --shell accepts to what a golden records. sh is the
// name of the thing being asked for on a Linux guest; posix is what the state
// file calls it, and both are accepted rather than one being a trap.
func shellFlag(v string) (string, error) {
	switch v {
	case "":
		return "", nil
	case "sh":
		return core.ShellPOSIX, nil
	}
	if !core.ValidShell(v) {
		return "", fmt.Errorf("unknown --shell %q: one of powershell, cmd, sh", v)
	}
	return v, nil
}

// execCommand builds the one command string an SSH session carries. want is
// the shell that has to read the command; have is the shell sshd will hand
// the line to. Equal, the quoting is the whole job. Different, the line has to
// start the wanted shell - and the guest's own shell parses it first, so the
// wrapper is quoted for have while its payload is quoted for want.
func execCommand(want, have string, argv []string) string {
	if want == have {
		return quoteFor(want, argv)
	}
	switch want {
	case core.ShellPowerShell:
		return core.LaunchPowerShell(have, sshx.QuotePowerShell(argv))
	case core.ShellCmd:
		return core.LaunchCmd(have, strings.Join(argv, " "))
	default:
		return core.LaunchSh(have, sshx.QuotePOSIX(argv))
	}
}

func quoteFor(shell string, argv []string) string {
	switch shell {
	case core.ShellPowerShell:
		return sshx.QuotePowerShell(argv)
	case core.ShellCmd:
		// cmd.exe has no quoting that rebuilds an argv - 'dir' 'C:\x' is no
		// command at all there - so its words go over joined, as typed.
		return strings.Join(argv, " ")
	default:
		return sshx.QuotePOSIX(argv)
	}
}

func init() {
	execCmd.Flags().DurationVar(&execTimeout, "timeout", core.DefaultExecTimeout,
		"give up if the command has not finished in this long")
	execCmd.Flags().StringVar(&execShell, "shell", "",
		"run the command under this shell: powershell, cmd or sh")
	execCmd.Flags().BoolVar(&execScript, "stdin", false,
		"read a script from stdin and run it instead of a -- command")
	execCmd.Flags().BoolVar(&execKill, "kill-on-timeout", false,
		"kill the command and its children in the guest when the timeout fires")
	execCmd.Flags().BoolVar(&execDesk, "desktop", false,
		"run in the logged-in interactive session so the screen shows it (Windows guests)")
	rootCmd.AddCommand(execCmd)
}
