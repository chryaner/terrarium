package cli

import (
	"fmt"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var revertCmd = &cobra.Command{
	Use:   "revert <env>",
	Short: "Return an env to its clean snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		start := time.Now()
		if err := e.Revert(args[0], func(msg string) { fmt.Println(msg) }); err != nil {
			return err
		}
		fmt.Printf("clean and ready in %.0fs\n", time.Since(start).Seconds())
		return nil
	},
}

func init() { rootCmd.AddCommand(revertCmd) }
