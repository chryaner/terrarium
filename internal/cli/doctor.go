package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that terrarium can talk to VirtualBox",
	RunE: func(cmd *cobra.Command, args []string) error {
		checks := core.Doctor()
		for _, c := range checks {
			switch {
			case c.OK:
				fmt.Printf("ok    %s: %s\n", c.Name, c.Detail)
			case c.Optional:
				fmt.Printf("warn  %s: %s\n", c.Name, c.Detail)
			default:
				fmt.Printf("FAIL  %s: %s\n", c.Name, c.Detail)
			}
			if !c.OK && c.Fix != "" {
				fmt.Printf("      -> %s\n", c.Fix)
			}
		}

		if !core.DoctorOK(checks) {
			return fmt.Errorf("terrarium is not ready")
		}
		fmt.Println("\nterrarium is ready.")
		return nil
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }
