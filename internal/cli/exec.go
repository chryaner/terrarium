package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/spf13/cobra"
)

var execTimeout time.Duration

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

Windows guests are the other exception: their commands are joined for cmd.exe,
which has no single-quote syntax to reconstruct argv with.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
			return fmt.Errorf("usage: terrarium exec <env> -- <command...>")
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		port, user, password, key, err := e.SSHTarget(args[0])
		if err != nil {
			return err
		}
		// Argv is quoted so the guest shell rebuilds it exactly. Joining it
		// raw let that shell re-split the arguments: a `bash -c '...'` payload
		// word-split, and a | inside a sed expression became a remote pipe.
		//
		// The quoting is POSIX, and Windows OpenSSH runs commands through
		// cmd.exe, which does not strip single quotes - `'dir' 'C:\x'` would
		// be no command at all there. So Windows guests keep the raw join they
		// work with today, and the guest type is only looked up when quoting
		// changed something: `exec t1 -- uname -a` costs no VBoxManage call.
		command := sshx.QuotePOSIX(args[1:])
		if raw := strings.Join(args[1:], " "); command != raw {
			win, err := e.GuestIsWindows(args[0])
			if err != nil {
				return err
			}
			if win {
				command = raw
			}
		}
		code, err := sshx.ExecTimeout(context.Background(), execTimeout,
			port, user, password, key, command, os.Stdout, os.Stderr)
		if err != nil {
			return err
		}
		if code != 0 {
			os.Exit(code) // propagate the remote exit code
		}
		return nil
	},
}

func init() {
	execCmd.Flags().DurationVar(&execTimeout, "timeout", core.DefaultExecTimeout,
		"give up if the command has not finished in this long")
	rootCmd.AddCommand(execCmd)
}
