package cli

import (
	"fmt"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	importName     string
	importUser     string
	importPassword string
	importKey      string
	importCPUs     int
	importMemory   int
)

var importCmd = &cobra.Command{
	Use:   "import <file.ova>",
	Short: "Register an .ova appliance as a golden image",
	Long: `Imports an appliance file and records it as a golden that forks are
created from. Nothing is downloaded and nothing is seeded: unlike get, this
makes no assumption that the machine inside runs cloud-init, so a legacy
export imports instead of hanging waiting for one.

Credentials are optional. Import an appliance whose login you do not know,
fork it, read the login prompt with screenshot, try a guess with type, and
record what works with adopt.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if importName == "" {
			return fmt.Errorf("--name is required: it is the golden name forks are made from")
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		start := time.Now()
		g, err := e.ImportOVA(args[0], importName, importUser, importPassword, importKey,
			importCPUs, importMemory, func(msg string) { fmt.Println(msg) })
		if err != nil {
			return err
		}
		fmt.Printf("golden %s ready in %.0fs: `terrarium fork %s <name>`\n",
			importName, time.Since(start).Seconds(), importName)
		if g.SSHUser == "" {
			fmt.Println("note: no SSH user set; drive forks with screenshot/type/keys, and record what works with `" +
				core.AdoptHint(g.VMName, importName) + "`")
		}
		return nil
	},
}

func init() {
	importCmd.Flags().StringVar(&importName, "name", "", "golden name to record it under (required)")
	importCmd.Flags().StringVar(&importUser, "user", "", "SSH user inside the guest")
	importCmd.Flags().StringVar(&importPassword, "password", "", "SSH password inside the guest")
	importCmd.Flags().StringVar(&importKey, "key", "", "SSH private key path")
	importCmd.Flags().IntVar(&importCPUs, "cpus", 0, "CPUs (default: what the appliance shipped)")
	importCmd.Flags().IntVar(&importMemory, "memory", 0, "memory in MB (default: what the appliance shipped)")
	rootCmd.AddCommand(importCmd)
}
