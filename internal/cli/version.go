package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

// version prints the bare version so it is easy to script against. The root
// command's --version flag stays available too, printing the cobra-formatted
// "terrarium version X" line.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the terrarium version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(core.Version)
		return nil
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
