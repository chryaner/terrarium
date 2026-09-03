package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	forkTTL   time.Duration
	forkShare string
)

var forkCmd = &cobra.Command{
	Use:   "fork <golden> <name>",
	Short: "Create a disposable env from a golden image",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		opts := core.ForkOpts{TTL: forkTTL}
		if forkShare != "" {
			// VirtualBox needs an absolute host path, so `--share .` is
			// resolved here rather than refused.
			if opts.ShareHostPath, err = filepath.Abs(forkShare); err != nil {
				return err
			}
		}
		start := time.Now()
		// Fork cleans up after itself, so a failure here needs no rm hint.
		env, err := e.Fork(args[0], args[1], opts, func(msg string) { fmt.Println(msg) })
		if err != nil {
			return err
		}
		// Same as the MCP env_fork tool, so the env shows up in ~/.ssh/config
		// however it was created.
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return err
		}
		fmt.Printf("ready in %.0fs: `terrarium ssh %s` (port %d)\n",
			time.Since(start).Seconds(), args[1], env.SSHPort)
		if env.Share != "" {
			// The resolved host path, not just the guest one: a share is a
			// hole in the isolation and the user should see which one.
			fmt.Printf("sharing %s -> %s in the guest\n", env.Share, core.GuestSharePath)
		}
		return nil
	},
}

func init() {
	forkCmd.Flags().DurationVar(&forkTTL, "ttl", 0, "auto-remove after this long (e.g. 90m, 2h); `terrarium gc` collects it")
	forkCmd.Flags().StringVar(&forkShare, "share", "", "host directory to share into the guest at "+core.GuestSharePath)
	rootCmd.AddCommand(forkCmd)
}
