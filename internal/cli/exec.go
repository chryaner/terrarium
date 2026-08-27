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
		// Space-joined so the guest shell parses the result, exactly as
		// `ssh host cmd args...` does: this is what lets `-- 'a && b'`, pipes
		// and redirects work. Quote for the remote shell when you need to.
		command := strings.Join(args[1:], " ")
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
