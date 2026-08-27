package cli

import (
	"github.com/chryaner/terrarium/internal/mcpserver"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the MCP server (stdio) for AI agents",
	Long: `Speaks the Model Context Protocol over stdin/stdout so an AI agent can list,
fork, run commands in and destroy environments. Launched by the MCP client,
not run by hand.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpserver.Serve()
	},
}

func init() { rootCmd.AddCommand(mcpCmd) }
