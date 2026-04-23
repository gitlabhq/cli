package done

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams

	todoID int64
	all    bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "done [<id>] [flags]",
		Short: "Mark a to-do item as done.",
		Long:  ``,
		Example: heredoc.Doc(`
			glab todo done 123
			glab todo done --all
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	cmd.Flags().BoolVar(&opts.all, "all", false, "Mark all pending to-do items as done. (default false)")

	return cmd
}

func (o *options) complete(args []string) error {
	if len(args) > 0 {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return cmdutils.FlagError{
				Err: fmt.Errorf("invalid to-do ID: %q", args[0]),
			}
		}
		o.todoID = id
	}
	return nil
}

func (o *options) validate() error {
	if o.all && o.todoID != 0 {
		return cmdutils.FlagError{
			Err: fmt.Errorf("--all cannot be used with a to-do ID"),
		}
	}
	if !o.all && o.todoID == 0 {
		return cmdutils.FlagError{
			Err: fmt.Errorf("either a to-do ID or --all is required"),
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

	cs := o.io.Color()

	if o.all {
		_, err = client.Todos.MarkAllTodosAsDone()
		if err != nil {
			return cmdutils.WrapError(err, "failed to mark all to-do items as done.")
		}
		fmt.Fprintln(o.io.StdOut, cs.GreenCheck(), "All to-do items marked as done.")
		return nil
	}

	_, err = client.Todos.MarkTodoAsDone(o.todoID)
	if err != nil {
		return cmdutils.WrapError(err, "failed to mark to-do item as done.")
	}
	fmt.Fprintf(o.io.StdOut, "%s To-do item %d marked as done.\n", cs.GreenCheck(), o.todoID)

	return nil
}
