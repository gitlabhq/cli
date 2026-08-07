package list

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var (
	validStates  = map[string]bool{"pending": true, "done": true, "all": true}
	validActions = map[string]bool{
		"assigned": true, "mentioned": true, "build_failed": true,
		"marked": true, "approval_required": true, "directly_addressed": true,
	}
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams

	state   string
	action  string
	typ     string
	page    int
	perPage int

	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List your to-do items.",
		Long: heredoc.Docf(`
			By default, lists your pending to-do items. Use %[1]s--state%[1]s to
			show done or all items instead.

			Narrow the results with %[1]s--action%[1]s to filter by why an item was
			created, or %[1]s--type%[1]s to filter by its target type.
		`, "`"),
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
			glab todo list
			glab todo list --state=done
			glab todo list --action=assigned
			glab todo list --type=MergeRequest
			glab todo list --output=json
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.state, "state", "s", "pending", "Filter by state: pending, done, all.")
	fl.StringVarP(&opts.action, "action", "a", "", "Filter by action: assigned, mentioned, build_failed, marked, approval_required, directly_addressed.")
	fl.StringVarP(&opts.typ, "type", "t", "", "Filter by target type: Issue, MergeRequest.")
	fl.IntVarP(&opts.page, "page", "p", 1, "Page number.")
	fl.IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) validate() error {
	if !validStates[o.state] {
		return cmdutils.FlagError{
			Err: fmt.Errorf("invalid --state %q: must be one of: pending, done, all", o.state),
		}
	}
	if o.action != "" && !validActions[o.action] {
		return cmdutils.FlagError{
			Err: fmt.Errorf("invalid --action %q: must be one of: assigned, mentioned, build_failed, marked, approval_required, directly_addressed", o.action),
		}
	}
	if o.page < 1 {
		return cmdutils.FlagError{
			Err: fmt.Errorf("--page must be >= 1"),
		}
	}
	if o.perPage < 1 || o.perPage > 100 {
		return cmdutils.FlagError{
			Err: fmt.Errorf("--per-page must be between 1 and 100"),
		}
	}
	return nil
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	listOpts := &gitlab.ListTodosOptions{
		ListOptions: gitlab.ListOptions{
			Page:    int64(o.page),
			PerPage: int64(o.perPage),
		},
	}

	if o.state != "all" {
		listOpts.State = &o.state
	}
	if o.action != "" {
		action := gitlab.TodoAction(o.action)
		listOpts.Action = &action
	}
	if o.typ != "" {
		listOpts.Type = &o.typ
	}

	todos, _, err := client.Todos.ListTodos(listOpts)
	if err != nil {
		return cmdutils.WrapError(err, "failed to list to-do items.")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(todos)
	}

	cs := o.io.Color()
	table := tableprinter.NewTablePrinter()
	isTTY := o.io.IsOutputTTY()

	if len(todos) > 0 {
		table.AddRow("ID", "Action", "Type", "Title", "Project", "Created")
	}

	for _, todo := range todos {
		createdAt := ""
		if todo.CreatedAt != nil {
			if isTTY {
				createdAt = utils.TimeToPrettyTimeAgo(*todo.CreatedAt)
			} else {
				createdAt = todo.CreatedAt.String()
			}
		}

		projectPath := ""
		if todo.Project != nil {
			projectPath = todo.Project.PathWithNamespace
		}

		title := ""
		if todo.Target != nil {
			title = todo.Target.Title
		}

		table.AddRow(
			todo.ID,
			string(todo.ActionName),
			string(todo.TargetType),
			title,
			projectPath,
			cs.Gray(createdAt),
		)
	}

	o.io.LogInfo(table.String())

	return nil
}
