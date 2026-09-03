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
		if err := refreshSSHConfig(e.St); err != nil {
			return err
		}
		fmt.Print(core.RenderSSHConfig(e.St))
		return nil
	},
}

// refreshSSHConfig rewrites the managed section and names the goldens whose
// entries plain ssh and scp will still prompt for. Every command that touches
// the config goes through here, so the warning cannot be reached by one route
// and missed by another. It goes to stderr: `ssh-config` prints the config
// itself on stdout, and that is what people pipe.
func refreshSSHConfig(st *core.State) error {
	if err := core.UpdateSSHConfig(st); err != nil {
		return err
	}
	for _, g := range core.PasswordAuthGoldens(st) {
		fmt.Fprintf(os.Stderr, "warning: golden %s has a password and no key, so plain ssh and scp to its envs will prompt; terrarium ssh and terrarium exec will not\n", g)
	}
	return nil
}

func init() { rootCmd.AddCommand(sshConfigCmd) }
