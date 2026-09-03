package cli

import (
	"fmt"
	"os"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var sshConfigCmd = &cobra.Command{
	Use:   "ssh-config",
	Short: "Refresh the terrarium section of ~/.ssh/config",
	Long: `Rewrites the block between the terrarium markers in ~/.ssh/config so every
env is reachable as ` + "`ssh <env>`" + ` and shows up in VS Code Remote-SSH. Anything
outside the markers is left alone.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return err
		}
		warnPasswordAuth(e.St)
		fmt.Print(core.RenderSSHConfig(e.St))
		return nil
	},
}

// warnPasswordAuth names the goldens whose entries plain ssh and scp will
// still prompt for. Only ssh-config says it: fork, rm and gc rewrite the file
// too, but repeating the line on every one of those runs is noise, and the
// MCP server rewrites it with no terminal to warn on. It goes to stderr
// because the config itself is what people pipe from stdout.
func warnPasswordAuth(st *core.State) {
	for _, g := range core.PasswordAuthGoldens(st) {
		fmt.Fprintf(os.Stderr, "warning: golden %s has a password and no key, so plain ssh and scp to its envs will prompt; terrarium ssh and terrarium exec will not\n", g)
	}
}

func init() { rootCmd.AddCommand(sshConfigCmd) }
