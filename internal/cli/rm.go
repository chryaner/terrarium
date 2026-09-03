package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var rmGolden bool

var rmCmd = &cobra.Command{
	Use:   "rm <env>",
	Short: "Destroy an env and delete its disks",
	Long: `Destroys an env and deletes its disks. With --golden the name is a golden
image instead: its record goes, and its VM and disks too when terrarium built
them (an adopted VM is the user's and is left registered). A golden with forks
cannot be removed until they are.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if rmGolden {
			if err := e.RemoveGolden(args[0]); err != nil {
				return err
			}
			fmt.Printf("removed golden %s\n", args[0])
			return nil
		}
		if err := e.Remove(args[0]); err != nil {
			return err
		}
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", args[0])
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVar(&rmGolden, "golden", false, "remove a golden image instead of an env")
	rootCmd.AddCommand(rmCmd)
}
