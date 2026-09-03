package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var gcDryRun bool

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Remove expired and dangling envs",
	Long: `Removes envs whose TTL has passed (set at fork time with --ttl) and env
records whose VM no longer exists in VirtualBox. Envs forked without a TTL are
left alone however old they are. Use --dry-run to see what would go first.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		removals, err := e.GC(gcDryRun, func(msg string) { fmt.Println(msg) })
		if err != nil {
			return err
		}
		if len(removals) == 0 {
			fmt.Println("nothing to collect")
			return nil
		}
		if gcDryRun {
			for _, r := range removals {
				fmt.Printf("would remove %s: %s\n", r.Name, r.Reason)
			}
			return nil
		}
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return err
		}
		fmt.Printf("removed %d env(s)\n", len(removals))
		return nil
	},
}

func init() {
	gcCmd.Flags().BoolVar(&gcDryRun, "dry-run", false, "list what would be removed without removing it")
	rootCmd.AddCommand(gcCmd)
}
