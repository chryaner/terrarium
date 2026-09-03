package cli

import (
	"fmt"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	cpRecursive bool
	cpParents   bool
)

var cpCmd = &cobra.Command{
	Use:   "cp <src> <dst>",
	Short: "Copy files between the host and an env",
	Long: `Copies files over SFTP on the env's own SSH connection, with the credentials
its golden already holds. Exactly one side is <env>:<path>:

  terrarium cp ./build.tar t1:/tmp/build.tar    # host -> guest
  terrarium cp t1:/var/log/syslog ./syslog      # guest -> host
  terrarium cp -r ./src t1:/home/terrarium/src  # directories need -r

Guest paths always use forward slashes, Windows guests included:

  terrarium cp setup.exe win1:C:/Users/terrarium/setup.exe

A destination that is an existing directory receives the source under its own
name. The destination's parent directory must exist; -p creates it.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := e.Copy(args[0], args[1], cpRecursive, cpParents); err != nil {
			return err
		}
		fmt.Printf("%s -> %s\n", args[0], args[1])
		return nil
	},
}

func init() {
	cpCmd.Flags().BoolVarP(&cpRecursive, "recursive", "r", false, "copy directories")
	cpCmd.Flags().BoolVarP(&cpParents, "parents", "p", false, "create the destination's parent directories")
	rootCmd.AddCommand(cpCmd)
}
