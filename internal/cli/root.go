package cli

import (
	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "terrarium",
	Short:   "Disposable VirtualBox dev environments",
	Version: core.Version,
	Long: `terrarium turns VirtualBox golden images into disposable dev environments:
fork a machine in a fraction of a second, be working in seconds, revert the
moment you break something.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error { return rootCmd.Execute() }
