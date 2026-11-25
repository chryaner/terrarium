package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	lsJSON    bool
	lsGoldens bool
	lsEnvs    bool
)

type lsRow struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Role    string `json:"role,omitempty"`
	OSType  string `json:"ostype,omitempty"`
	Arch    string `json:"arch,omitempty"`
	UUID    string `json:"uuid"`
	SSHPort int    `json:"ssh_port,omitempty"`
}

// unknownOS is what the OS column shows for a VM terrarium does not manage:
// listing every machine's guest type would cost a VBoxManage call each, and
// only the ones that can be forked are worth the call.
const unknownOS = "-"

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List VMs, marking goldens and envs",
	Long: `Lists every VirtualBox VM and marks the ones terrarium manages: a golden as
golden:<name>, an env as env:<name>. Those names, not the VM names, are what
fork, exec and the other commands take.

ARCH comes from the guest type VirtualBox records: x86 is a 32-bit guest,
whatever the golden is called.

  terrarium ls --goldens   # only what can be forked
  terrarium ls --envs      # only envs`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		vms, err := e.VB.ListVMs()
		if err != nil {
			return err
		}
		// The guest type is why this column exists, so fill in the records
		// written before terrarium stored it rather than showing them blank.
		if err := e.FillOSTypes(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record guest types: %v\n", err)
		}

		role := map[string]lsRow{}
		for name, g := range e.St.Goldens {
			role[g.UUID] = lsRow{Role: "golden:" + name, OSType: g.OSType}
		}
		for name, env := range e.St.Envs {
			role[env.UUID] = lsRow{Role: "env:" + name, OSType: env.OSType, SSHPort: env.SSHPort}
		}

		rows := []lsRow{}
		for _, vm := range vms {
			state := "off"
			if vm.Running {
				state = "running"
			}
			r := role[vm.UUID]
			if !lsWanted(r.Role, lsGoldens, lsEnvs) {
				continue
			}
			rows = append(rows, lsRow{
				Name: vm.Name, State: state, Role: r.Role,
				OSType: r.OSType, Arch: core.ArchOf(r.OSType), UUID: vm.UUID, SSHPort: r.SSHPort,
			})
		}

		if lsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATE\tROLE\tOS\tARCH\tUUID")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				r.Name, r.State, r.Role, orDash(r.OSType), orDash(r.Arch), r.UUID)
		}
		return w.Flush()
	},
}

func orDash(s string) string {
	if s == "" {
		return unknownOS
	}
	return s
}

// lsWanted applies --goldens and --envs to one row. With neither flag every
// VM is listed, managed or not; with both, everything terrarium manages.
func lsWanted(role string, goldens, envs bool) bool {
	if !goldens && !envs {
		return true
	}
	return (goldens && strings.HasPrefix(role, "golden:")) || (envs && strings.HasPrefix(role, "env:"))
}

func init() {
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "output as JSON")
	lsCmd.Flags().BoolVar(&lsGoldens, "goldens", false, "list only golden images")
	lsCmd.Flags().BoolVar(&lsEnvs, "envs", false, "list only envs")
	rootCmd.AddCommand(lsCmd)
}
