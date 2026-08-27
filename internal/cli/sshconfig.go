package cli

import (
	"fmt"

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
		fmt.Print(core.RenderSSHConfig(e.St))
		return nil
	},
}

func init() { rootCmd.AddCommand(sshConfigCmd) }
