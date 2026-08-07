package serve

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmdServe(_ cmdutils.Factory) *cobra.Command {
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a MCP server with stdio transport. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Start a Model Context Protocol server to expose GitLab features
			as tools for AI assistants like Claude Code.

			The server uses stdio (standard input and output) transport for
			communication, and provides tools to:

			- Manage issues (list, create, update, close, add notes)
			- Manage merge requests (list, create, update, merge, add notes)
			- Manage projects (list, get details)
			- Manage CI/CD pipelines and jobs

			To configure this server in Claude Code, add this code to your
			MCP settings:

			%[1]sjson
			{
			  "mcpServers": {
			    "glab": {
			      "type": "stdio",
			      "command": "glab",
			      "args": ["mcp", "serve"]
			    }
			  }
			}
			%[1]s
		`, "```") + text.ExperimentalString,
		Example: heredoc.Doc(`
			glab mcp serve`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get the root command by traversing up the parent chain
			rootCmd := cmd
			for rootCmd.Parent() != nil {
				rootCmd = rootCmd.Parent()
			}

			// Initialize the MCP server
			server := newMCPServer(rootCmd)

			// Run the server with context
			if err := server.Run(cmd.Context()); err != nil {
				return fmt.Errorf("MCP server error: %w", err)
			}

			return nil
		},
	}

	return serveCmd
}
