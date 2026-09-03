package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var lsJSON bool

type lsRow struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Role    string `json:"role,omitempty"`
	OSType  string `json:"ostype,omitempty"`
	UUID    string `json:"uuid"`
	SSHPort int    `json:"ssh_port,omitempty"`
}

// unknownOS is what the OS column shows for a VM terrarium does not manage:
// listing every machine's guest type would cost a VBoxManage call each, and
// only the ones that can be forked are worth the call.
const unknownOS = "-"

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List VMs, marking goldens and envs",
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

		var rows []lsRow
		for _, vm := range vms {
			state := "off"
			if vm.Running {
				state = "running"
			}
			r := role[vm.UUID]
			rows = append(rows, lsRow{
				Name: vm.Name, State: state, Role: r.Role,
				OSType: r.OSType, UUID: vm.UUID, SSHPort: r.SSHPort,
			})
		}

		if lsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATE\tROLE\tOS\tUUID")
		for _, r := range rows {
			ostype := r.OSType
			if ostype == "" {
				ostype = unknownOS
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.State, r.Role, ostype, r.UUID)
		}
		return w.Flush()
	},
}

func init() {
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(lsCmd)
}
