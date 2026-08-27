package cli

import (
	"os"
	"os/exec"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/spf13/cobra"
)

var sshCmd = &cobra.Command{
	Use:   "ssh <env>",
	Short: "Open an interactive shell in an env",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		// ssh.exe prompts for the password itself when no key is set
		port, user, _, key, err := e.SSHTarget(args[0])
		if err != nil {
			return err
		}
		c := exec.Command("ssh", sshx.Args(port, user, key)...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	},
}

func init() { rootCmd.AddCommand(sshCmd) }
