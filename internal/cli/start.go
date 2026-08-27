package cli

import (
	"fmt"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start [env]",
	Short: "Boot a stopped env and wait until it is ready",
	Long: `Starts an env that was shut down with ` + "`terrarium down`" + ` and waits for it
to answer SSH (a credless env returns as soon as it is booting). Does nothing
if it is already running. With no argument, uses the project in this directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := envOrProject(args)
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		start := time.Now()
		if _, err := e.Start(name, func(msg string) { fmt.Println(msg) }); err != nil {
			return err
		}
		fmt.Printf("%s is up in %.0fs (`terrarium ssh %s`)\n", name, time.Since(start).Seconds(), name)
		return nil
	},
}

func init() { rootCmd.AddCommand(startCmd) }
