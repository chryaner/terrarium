package cli

import (
	"fmt"
	"path/filepath"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	createISO    string
	createOSType string
	createDiskGB int
	createCPUs   int
	createMemory int
)

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a blank env from an ISO and boot its installer",
	Long: `Creates a blank machine with the ISO in its DVD drive and boots it, for an OS
with no cloud image and no unattended installer - an old distribution, or one
whose installer has to be answered by hand.

The result is an env with no golden and no credentials, so exec and ssh do not
work on it. Drive the installer with screenshot, type, keys and click; revert
puts the blank disk back and restarts the install. Once the OS is installed:

  terrarium promote <name> <image>
  terrarium adopt trr-golden-<image> --user <user> --password <pw>

The boot order is disk first, DVD second, so the installer runs while the disk
is blank and the installed system boots itself afterwards.

--ostype is a VirtualBox guest type; ` + "`VBoxManage list ostypes`" + ` names them.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		// Left empty when the flag is: filepath.Abs("") is the working
		// directory, which would pass the "does the ISO exist" check.
		iso := createISO
		if iso != "" {
			if iso, err = filepath.Abs(iso); err != nil {
				return err
			}
		}
		env, err := e.Create(args[0], core.CreateOpts{
			ISO:    iso,
			OSType: createOSType,
			DiskGB: createDiskGB,
			CPUs:   createCPUs,
			MemMB:  createMemory,
		}, func(msg string) { fmt.Println(msg) })
		if err != nil {
			return err
		}
		fmt.Printf("%s is booting the installer: `terrarium screenshot %s` to see it (port %d reserved for SSH)\n",
			args[0], args[0], env.SSHPort)
		return nil
	},
}

func init() {
	createCmd.Flags().StringVar(&createISO, "iso", "", "installation ISO to boot (required)")
	createCmd.Flags().StringVar(&createOSType, "ostype", "", "VirtualBox guest type, e.g. Linux_64 or Windows10_64 (required)")
	createCmd.Flags().IntVar(&createDiskGB, "disk-gb", core.DefaultDiskGB, "size of the blank disk in GB (dynamic, grows on demand)")
	createCmd.Flags().IntVar(&createCPUs, "cpus", core.DefaultCPUs, "CPUs")
	createCmd.Flags().IntVar(&createMemory, "memory", core.DefaultMemoryMB, "memory in MB")
	rootCmd.AddCommand(createCmd)
}
