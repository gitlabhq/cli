package timelog

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/timelog/list"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timelog <command> [flags]",
		Short: "Report on time logged on issues and merge requests.",
		Long: heredoc.Docf(`
			Time logs are the entries that time tracking records when you log time on an issue or merge request. Use %[1]sglab timelog list%[1]s to see who logged time, on which items, and the total, for a project or group over a date range.

			To log time on an issue, use %[1]sglab issue update --spend%[1]s.
		`, "`"),
	}

	cmd.AddCommand(list.NewCmd(f))
	return cmd
}
