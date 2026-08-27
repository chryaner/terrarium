package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down [env]",
	Short: "Shut an env down, keeping its disk and state",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := envOrProject(args)
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := e.Down(name); err != nil {
			return err
		}
		fmt.Printf("%s is down (`terrarium start %s` boots it again)\n", name, name)
		return nil
	},
}

func init() { rootCmd.AddCommand(downCmd) }
