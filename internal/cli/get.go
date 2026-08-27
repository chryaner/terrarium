package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/recipe"
	"github.com/spf13/cobra"
)

var (
	getCPUs   int
	getMemory int
	getForce  bool
)

var getCmd = &cobra.Command{
	Use:   "get <image>",
	Short: "Download a cloud image and build a golden from it",
	Long: `Downloads the official cloud image named by a recipe, generates an SSH
keypair and a cloud-init seed for it, imports it, boots it once so cloud-init
applies the seed, then shuts it down and snapshots it. The result is a golden
image ready to fork.

Available images: ` + strings.Join(recipe.Names(), ", "),
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		// Zero means "no opinion", which is what lets a Windows image default
		// higher. An explicit --cpus 2 must not be mistaken for silence.
		cpus, memory := 0, 0
		if cmd.Flags().Changed("cpus") {
			cpus = getCPUs
		}
		if cmd.Flags().Changed("memory") {
			memory = getMemory
		}

		start := time.Now()
		if _, err := e.Get(args[0], cpus, memory, getForce, func(msg string) { fmt.Println(msg) }); err != nil {
			return err
		}
		fmt.Printf("golden ready in %.0fs: `terrarium fork %s <name>`\n",
			time.Since(start).Seconds(), args[0])
		return nil
	},
}

func init() {
	getCmd.Flags().IntVar(&getCPUs, "cpus", core.DefaultCPUs, "CPUs for the golden image (Windows images get more)")
	getCmd.Flags().IntVar(&getMemory, "memory", core.DefaultMemoryMB, "memory in MB for the golden image (Windows images get more)")
	getCmd.Flags().BoolVar(&getForce, "force", false, "rebuild an existing golden from the cached image")
	rootCmd.AddCommand(getCmd)
}
