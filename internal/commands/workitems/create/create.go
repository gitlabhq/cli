package create

import (
	"context"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/api"
	"gitlab.com/gitlab-org/cli/internal/commands/workitems/utils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	// Dependencies
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	config       func() config.Config

	// Flags
	group        string
	title        string
	workItemType string
	description  string
	attach       []string
	confidential bool

	// Internal state
	needsPrompt bool
	scope       *api.ScopeInfo

	// Output
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
		config:       f.Config,
	}

	cmd := &cobra.Command{
		Use:   "create [flags]",
		Short: "Create work items in a project or group. (EXPERIMENTAL)",
		Long: heredoc.Docf(`Use %[1]s--type%[1]s to specify the kind of work item to create.
		The command uses your repository context to detect scope automatically.

		%[1]s--attach%[1]s uploads a file and references it at the end of the description. Repeat the flag for more than one file, or pass %[1]s-%[1]s to read the file from standard input. Uploads are project-scoped, so %[1]s--attach%[1]s cannot be combined with %[1]s--group%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Create a work item in the current project
			glab work-items create --type issue

			# Create a work item in a group
			glab work-items create --type epic --group my-group

			# Read the description from a file
			glab work-items create --type issue --title "Add feature" --description-file description.md

			# Read the description from standard input
			cat description.md | glab work-items create --type issue --title "Add feature" --description-file -

			# Attach a screenshot to the description
			glab work-items create --type issue --title "Add feature" --attach ./screenshot.png
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Context(), cmd); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	// Enable -R flag for repo override
	cmdutils.EnableRepoOverride(cmd, f)

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	// Flags
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Create work items for a group or subgroup.")
	cmd.Flags().StringVarP(&opts.workItemType, "type", "T", "", "Type of work item ("+strings.Join(utils.ValidTypeNames(), ", ")+").")

	cmd.Flags().StringVarP(&opts.title, "title", "t", "", "Add a title for the work item.")
	cmd.Flags().StringVarP(&opts.description, "description", "d", "", "Description of the work item. Set to \"-\" to open an editor.")
	cmd.Flags().BoolVarP(&opts.confidential, "confidential", "c", false, "Mark work item confidential.")

	cmdutils.AddDescriptionFileFlag(cmd, "work item")
	cmdutils.AddAttachFlag(cmd, &opts.attach, "description")

	_ = cmd.MarkFlagRequired("type")
	cmd.MarkFlagsMutuallyExclusive("group", "repo")
	// Uploads are project-scoped, and GitLab has no group-level equivalent.
	cmd.MarkFlagsMutuallyExclusive("group", "attach")

	return cmd
}

func (opts *options) complete(ctx context.Context, cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	opts.group = group
	opts.needsPrompt = !cmd.Flags().Changed("title")

	if err := cmdutils.ResolveDescriptionFile(opts.io, cmd); err != nil {
		return err
	}

	if err := cmdutils.HandleDescriptionEditor(ctx, &opts.description, opts.io, opts.config, nil); err != nil {
		return err
	}

	scope, err := utils.DetectScope(opts.group, opts.baseRepo)
	if err != nil {
		return err
	}
	opts.scope = scope

	return nil
}

func (opts *options) validate() error {
	if _, err := utils.ResolveTypeID(opts.workItemType); err != nil {
		return cmdutils.FlagError{Err: err}
	}

	if opts.needsPrompt && !opts.io.IsInteractive() {
		return cmdutils.FlagError{Err: fmt.Errorf("--title required for non-interactive mode")}
	}

	return nil
}

func (opts *options) run(ctx context.Context) error {
	client, err := opts.gitlabClient()
	if err != nil {
		return fmt.Errorf("failed to get GitLab client: %w", err)
	}

	typeID, err := utils.ResolveTypeID(opts.workItemType)
	if err != nil {
		return err
	}

	createOpts := &gitlab.CreateWorkItemOptions{
		Title: opts.title,
	}

	description, err := cmdutils.AppendAttachments(ctx, opts.io, client, opts.scope.Path, opts.description, opts.attach)
	if err != nil {
		return err
	}
	if description != "" {
		createOpts.Description = new(description)
	}

	if opts.confidential {
		createOpts.Confidential = new(true)
	}

	wi, _, err := client.WorkItems.CreateWorkItem(opts.scope.Path, typeID, createOpts, gitlab.WithContext(ctx))
	if err != nil {
		return err
	}

	switch opts.outputFormat {
	case "json":
		return opts.io.PrintJSON(wi)
	default:
		opts.io.LogInfo("- Creating work item in", opts.scope.Path)

		if opts.io.IsaTTY {
			opts.io.LogInfof("#%d %s\n%s\n", wi.IID, wi.Title, wi.WebURL)
		} else {
			opts.io.LogInfo(wi.WebURL)
		}
	}

	return nil
}
