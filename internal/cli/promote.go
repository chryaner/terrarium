package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var promoteCmd = &cobra.Command{
	Use:   "promote <env> <image>",
	Short: "Flatten an env into a new golden image",
	Long: `Copies the env's current state into a new standalone golden, so a machine you
configured by hand becomes a fork source like any other image. The copy is
full, not linked: it takes minutes and the disk of a golden, and afterwards
depends on nothing. The env is shut down and left in place.

To make the state shareable rather than local, write the same steps as a
derived recipe (a YAML file with from: and setup:) and 'terrarium get' it.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if _, err := e.Promote(args[0], args[1], func(msg string) { fmt.Println(msg) }); err != nil {
			return err
		}
		fmt.Printf("%s is a golden now: `terrarium fork %s <name>` forks it\n", args[1], args[1])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(promoteCmd)
}
