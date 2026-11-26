package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	forkTTL      time.Duration
	forkShare    string
	forkCPUs     int
	forkMemory   int
	forkNewMAC   bool
	forkHostname string
)

var forkCmd = &cobra.Command{
	Use:   "fork <golden> <name>",
	Short: "Create a disposable env from a golden image",
	Long: `Clones a golden image into a new env, boots it, waits until a command can run
in it, and snapshots that clean state for ` + "`terrarium revert`" + `.

An env is the golden's twin: same host name, same network card address. That
is what most guests expect, and it does not matter while each env sits behind
its own NAT. Where envs meet - registering with the same server, say - give
each its own identity:

  terrarium fork ubuntu-24.04 agent1 --hostname agent1 --new-mac

Linux images often name the network card by its address in their own
settings, and would come up with no network after the change. So for a Linux
guest terrarium can log in to, --new-mac first removes the address from those
settings and restarts once. A guest it cannot log in to gets the new address
before its first boot, and has to cope with it by itself.

A fork that fails is removed again. The error quotes the end of the guest's
serial console, and that log and a screenshot are kept in the data directory
under failed/.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		opts := core.ForkOpts{
			TTL: forkTTL, CPUs: forkCPUs, MemMB: forkMemory,
			NewMAC: forkNewMAC, Hostname: forkHostname,
		}
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
	forkCmd.Flags().DurationVar(&forkTTL, "ttl", 0, "auto-remove after this long (e.g. 90m, 2h); `terrarium gc` collects it. Without it the env stays until `terrarium rm`")
	forkCmd.Flags().StringVar(&forkShare, "share", "", "host directory to share into the guest at "+core.GuestSharePath)
	forkCmd.Flags().IntVar(&forkCPUs, "cpus", 0, "CPUs (default: the golden's)")
	forkCmd.Flags().IntVar(&forkMemory, "memory", 0, "memory in MB (default: the golden's)")
	forkCmd.Flags().BoolVar(&forkNewMAC, "new-mac", false, "give the env its own network card addresses instead of the golden's")
	forkCmd.Flags().StringVar(&forkHostname, "hostname", "", "host name to give the guest (default: the golden's)")
	rootCmd.AddCommand(forkCmd)
}
