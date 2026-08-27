package cli

import (
	"fmt"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var forkTTL time.Duration

var forkCmd = &cobra.Command{
	Use:   "fork <golden> <name>",
	Short: "Create a disposable env from a golden image",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		start := time.Now()
		env, err := e.Fork(args[0], args[1], core.ForkOpts{TTL: forkTTL}, func(msg string) { fmt.Println(msg) })
		if err != nil {
			if env != nil {
				return fmt.Errorf("fork failed (clean up with `terrarium rm %s`): %w", args[1], err)
			}
			return err
		}
		// Same as the MCP env_fork tool, so the env shows up in ~/.ssh/config
		// however it was created.
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return err
		}
		fmt.Printf("ready in %.0fs: `terrarium ssh %s` (port %d)\n",
			time.Since(start).Seconds(), args[1], env.SSHPort)
		return nil
	},
}

func init() {
	forkCmd.Flags().DurationVar(&forkTTL, "ttl", 0, "auto-remove after this long (e.g. 90m, 2h); `terrarium gc` collects it")
	rootCmd.AddCommand(forkCmd)
}
