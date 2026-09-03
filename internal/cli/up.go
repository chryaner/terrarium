package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring up the env for the project in this directory",
	Long: `Reads ` + core.ProjectFile + ` from the current directory or a parent, then forks
the env it describes, shares the project folder into the guest at ` + core.GuestSharePath + `
and registers the env in ~/.ssh/config. Run again to start an env that is
already there.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		p, err := core.FindProject(cwd)
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}

		start := time.Now()
		// A failed fork rolls itself back inside the engine, so there is no
		// half-built env to tell the user about.
		env, existed, err := e.Up(p, func(msg string) { fmt.Println(msg) })
		if err != nil {
			return err
		}
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return err
		}

		if existed {
			fmt.Printf("%s already exists; `terrarium rm %s` then `terrarium up` to recreate it\n", p.Name, p.Name)
		}
		fmt.Printf("ready in %.0fs: `ssh %s` (or `terrarium ssh %s`)\n",
			time.Since(start).Seconds(), p.Name, p.Name)
		if env.Share != "" {
			// The resolved host path, not just the guest one: a share is a
			// hole in the isolation and the user should see which one.
			fmt.Printf("sharing %s -> %s in the guest\n", env.Share, core.GuestSharePath)
		}
		return nil
	},
}

// envOrProject resolves the env name from an argument, or from the project
// config when the user just typed the command inside a project.
func envOrProject(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	p, err := core.FindProject(cwd)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

func init() { rootCmd.AddCommand(upCmd) }
