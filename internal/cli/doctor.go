package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var doctorGoldens bool

var doctorCmd = &cobra.Command{
	Use:   "doctor [golden...]",
	Short: "Check that terrarium can talk to VirtualBox",
	Long: `Checks the host: VirtualBox, its service, the data directory, an ssh client.

That says nothing about the golden images, which can go bad while switched
off - a Windows password expires after 42 days and the guest then refuses the
login. --goldens proves each one the slow, certain way: it forks a throwaway
env, runs a command in it and removes it.

  terrarium doctor                    # the host, in a second
  terrarium doctor --goldens          # and every golden, about half a minute each
  terrarium doctor --goldens win10 win11   # and only these`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && !doctorGoldens {
			return fmt.Errorf("to check golden images, add --goldens: `terrarium doctor --goldens %s`", args[0])
		}
		checks := core.Doctor()
		printChecks(checks)
		ok := core.DoctorOK(checks)

		if doctorGoldens && ok {
			e, err := core.NewEngine()
			if err != nil {
				return err
			}
			fmt.Println()
			goldens := e.CheckGoldens(args, func(c core.Check) { printChecks([]core.Check{c}) })
			ok = core.DoctorOK(goldens)
		}

		if !ok {
			return fmt.Errorf("terrarium is not ready")
		}
		fmt.Println("\nterrarium is ready.")
		return nil
	},
}

func printChecks(checks []core.Check) {
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
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorGoldens, "goldens", false, "also fork each golden image and run a command in it")
	rootCmd.AddCommand(doctorCmd)
}
