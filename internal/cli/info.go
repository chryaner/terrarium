package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var infoJSON bool

var infoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Report what a golden or env actually is",
	Long: `Reports the guest type and architecture, hardware, snapshot and how
terrarium logs in. Read this before forking a golden: an image built from a
32-bit recipe will not run x64 software, and that is not visible anywhere
else until something fails inside the guest.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		in, err := e.Info(args[0])
		if err != nil {
			return err
		}
		if infoJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(in)
		}
		fmt.Print(core.FormatInfo(in))
		return nil
	},
}

func init() {
	infoCmd.Flags().BoolVar(&infoJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(infoCmd)
}
