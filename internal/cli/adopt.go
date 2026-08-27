package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	adoptSnapshot string
	adoptUser     string
	adoptPassword string
	adoptKey      string
	adoptTake     bool
)

var adoptCmd = &cobra.Command{
	Use:   "adopt <vm>",
	Short: "Register an existing VM+snapshot as a golden image",
	Long: `Registers an existing VirtualBox VM as a golden image that forks are
created from. The VM itself is not modified unless --take-snapshot is given.
Re-running adopt updates the record, e.g. to set SSH credentials.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		g, err := e.Adopt(args[0], adoptSnapshot, adoptUser, adoptPassword, adoptKey, adoptTake)
		if err != nil {
			return err
		}
		fmt.Printf("adopted %s (snapshot %q)\n", g.VMName, g.Snapshot)
		if g.SSHUser == "" {
			fmt.Println("note: no SSH user set; exec/ssh need `terrarium adopt " + g.VMName + " --user <user> [--password <pw> | --key <path>]`")
		}
		return nil
	},
}

func init() {
	adoptCmd.Flags().StringVar(&adoptSnapshot, "snapshot", "", "snapshot to fork from (default "+core.DefaultSnapshot+")")
	adoptCmd.Flags().StringVar(&adoptUser, "user", "", "SSH user inside the guest")
	adoptCmd.Flags().StringVar(&adoptPassword, "password", "", "SSH password inside the guest")
	adoptCmd.Flags().StringVar(&adoptKey, "key", "", "SSH private key path")
	adoptCmd.Flags().BoolVar(&adoptTake, "take-snapshot", false, "create the snapshot if it does not exist")
	rootCmd.AddCommand(adoptCmd)
}
