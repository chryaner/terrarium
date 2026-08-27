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
	UUID    string `json:"uuid"`
	SSHPort int    `json:"ssh_port,omitempty"`
}

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

		role := map[string]lsRow{}
		for name, g := range e.St.Goldens {
			role[g.UUID] = lsRow{Role: "golden:" + name}
		}
		for name, env := range e.St.Envs {
			role[env.UUID] = lsRow{Role: "env:" + name, SSHPort: env.SSHPort}
		}

		var rows []lsRow
		for _, vm := range vms {
			state := "off"
			if vm.Running {
				state = "running"
			}
			r := role[vm.UUID]
			rows = append(rows, lsRow{Name: vm.Name, State: state, Role: r.Role, UUID: vm.UUID, SSHPort: r.SSHPort})
		}

		if lsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATE\tROLE\tUUID")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, r.State, r.Role, r.UUID)
		}
		return w.Flush()
	},
}

func init() {
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(lsCmd)
}
