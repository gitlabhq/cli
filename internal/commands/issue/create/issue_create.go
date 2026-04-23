package create

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issue/issueutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/recovery"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var createIssue = func(client *gitlab.Client, projectID any, opts *gitlab.CreateIssueOptions) (*gitlab.Issue, error) {
	issue, _, err := client.Issues.CreateIssue(projectID, opts)
	if err != nil {
		return nil, err
	}

	return issue, nil
}

type options struct {
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Assignees   []string `json:"assignees,omitempty"`

	Weight        int64  `json:"weight,omitempty"`
	Milestone     int64  `json:"milestone,omitempty"`
	LinkedMR      int64  `json:"linked_mr,omitempty"`
	LinkedIssues  []int  `json:"linked_issues,omitempty"`
	IssueLinkType string `json:"issue_link_type,omitempty"`
	TimeEstimate  string `json:"time_estimate,omitempty"`
	TimeSpent     string `json:"time_spent,omitempty"`
	EpicID        int64  `json:"epic_id,omitempty"`
	DueDate       string `json:"due_date,omitempty"`

	MilestoneFlag string `json:"milestone_flag"`

	IsConfidential bool `json:"is_confidential,omitempty"`

	noEditor    bool
	needsPrompt bool
	yes         bool
	web         bool
	recover     bool

	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	remotes      func() (glrepo.Remotes, error)
	config       func() config.Config

	baseProject *gitlab.Project
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
		remotes:      f.Remotes,
		config:       f.Config,
	}
	issueCreateCmd := &cobra.Command{
		Use:     "create [flags]",
		Short:   `Create an issue.`,
		Long:    ``,
		Aliases: []string{"new"},
		Example: heredoc.Doc(`
			glab issue create
			glab issue new
			glab issue create -m release-2.0.0 -t "we need this feature" --label important
			glab issue new -t "Fix CVE-YYYY-XXXX" -l security --linked-mr 123
			glab issue create -m release-1.0.1 -t "security fix" --label security --web --recover`),
		Args: cobra.ExactArgs(0),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.gitlabClient()
			if err != nil {
				return err
			}

			repo, err := opts.baseRepo()
			if err != nil {
				return err
			}
			hasTitle := cmd.Flags().Changed("title")
			hasDescription := cmd.Flags().Changed("description")

			// disable interactive mode if title and description are explicitly defined
			opts.needsPrompt = !(hasTitle && hasDescription)

			if opts.needsPrompt && !opts.io.IsInteractive() {
				return &cmdutils.FlagError{Err: errors.New("'--title' and '--description' required for non-interactive mode.")}
			}

			// Remove this once --yes does more than just skip the prompts that --web happen to skip
			// by design
			if opts.yes && opts.web {
				return &cmdutils.FlagError{Err: errors.New("'--web' already skips all prompts currently skipped by '--yes'.")}
			}

			opts.baseProject, err = api.GetProject(client, repo.FullName())
			if err != nil {
				return err
			}

			if !opts.baseProject.IssuesEnabled { //nolint:staticcheck
				fmt.Fprintf(opts.io.StdErr, "Issues are disabled for project %q or require project membership. ", opts.baseProject.PathWithNamespace)
				fmt.Fprintf(opts.io.StdErr, "Make sure issues are enabled for the %q project, and if required, you are a member of the project.\n",
					opts.baseProject.PathWithNamespace)
				return cmdutils.SilentError
			}

			if err := createRun(cmd.Context(), opts); err != nil {
				// always save options to file
				recoverErr := createRecoverSaveFile(repo.FullName(), opts)
				if recoverErr != nil {
					fmt.Fprintf(opts.io.StdErr, "Could not create recovery file: %v", recoverErr)
				}

				return err
			}

			return nil
		},
	}
	issueCreateCmd.Flags().StringVarP(&opts.Title, "title", "t", "", "Issue title.")
	issueCreateCmd.Flags().StringVarP(&opts.Description, "description", "d", "", "Issue description. Set to \"-\" to open an editor.")
	issueCreateCmd.Flags().StringSliceVarP(&opts.Labels, "label", "l", []string{}, "Add label by name. Multiple labels can be comma-separated or specified by repeating the flag.")
	issueCreateCmd.Flags().StringSliceVarP(&opts.Assignees, "assignee", "a", []string{}, "Assign issue to people by their `usernames`. Multiple usernames can be comma-separated or specified by repeating the flag.")
	issueCreateCmd.Flags().StringVarP(&opts.MilestoneFlag, "milestone", "m", "", "The global ID or title of a milestone to assign.")
	issueCreateCmd.Flags().BoolVarP(&opts.IsConfidential, "confidential", "c", false, "Set an issue to be confidential. (default false)")
	issueCreateCmd.Flags().Int64VarP(&opts.LinkedMR, "linked-mr", "", 0, "The IID of a merge request in which to resolve all issues.")
	issueCreateCmd.Flags().Int64VarP(&opts.Weight, "weight", "w", 0, "Issue weight. Valid values are greater than or equal to 0.")
	issueCreateCmd.Flags().BoolVarP(&opts.noEditor, "no-editor", "", false, "Don't open editor to enter a description. If set to true, uses prompt. (default false)")
	issueCreateCmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Don't prompt for confirmation to submit the issue.")
	issueCreateCmd.Flags().BoolVar(&opts.web, "web", false, "Continue issue creation with web interface.")
	issueCreateCmd.Flags().IntSliceVarP(&opts.LinkedIssues, "linked-issues", "", []int{}, "The IIDs of issues that this issue links to. Multiple IIDs can be comma-separated or specified by repeating the flag.")
	issueCreateCmd.Flags().StringVarP(&opts.IssueLinkType, "link-type", "", "relates_to", "Type for the issue link.")
	issueCreateCmd.Flags().StringVarP(&opts.TimeEstimate, "time-estimate", "e", "", "Set time estimate for the issue.")
	issueCreateCmd.Flags().StringVarP(&opts.TimeSpent, "time-spent", "s", "", "Set time spent for the issue.")
	issueCreateCmd.Flags().BoolVar(&opts.recover, "recover", false, "Save the options to a file if the issue fails to be created. If the file exists, the options will be loaded from the recovery file. (EXPERIMENTAL)")
	issueCreateCmd.Flags().Int64VarP(&opts.EpicID, "epic", "", 0, "ID of the epic to add the issue to.")
	issueCreateCmd.Flags().StringVarP(&opts.DueDate, "due-date", "", "", "A date in 'YYYY-MM-DD' format.")

	return issueCreateCmd
}

func (o *options) selectTemplate(ctx context.Context) (string, error) {
	templateNames, err := cmdutils.ListGitLabTemplates(cmdutils.IssueTemplate)
	if err != nil {
		return "", fmt.Errorf("error getting templates: %w", err)
	}

	const blankIssueOption = "Open a blank issue"
	templateNames = append(templateNames, blankIssueOption)

	var selectedTemplate string
	if err := o.io.Select(ctx, &selectedTemplate, "Choose a template", templateNames); err != nil {
		return "", fmt.Errorf("could not prompt: %w", err)
	}

	if selectedTemplate == blankIssueOption {
		return "", nil // Return empty string for blank issue
	}

	templateContents, err := cmdutils.LoadGitLabTemplate(cmdutils.IssueTemplate, selectedTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to get template contents: %w", err)
	}

	return templateContents, nil
}

var createRun = func(ctx context.Context, opts *options) error {
	apiClient, err := opts.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := opts.baseRepo()
	if err != nil {
		return err
	}

	var templateContents string

	issueCreateOpts := &gitlab.CreateIssueOptions{}

	if opts.MilestoneFlag != "" {
		opts.Milestone, err = cmdutils.ParseMilestone(apiClient, repo, opts.MilestoneFlag)
		if err != nil {
			return err
		}
	}

	if opts.recover {
		if err := recovery.FromFile(repo.FullName(), "issue.json", opts); err != nil {
			// if the file to recover doesn't exist, we can just ignore the error and move on
			if !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(opts.io.StdErr, "Failed to recover from file: %v", err)
			}
		} else {
			fmt.Fprintln(opts.io.StdOut, "Recovered create options from file.")
		}
	}

	// Handle -d- flag: show template selection first, then open editor
	err = cmdutils.HandleDescriptionEditor(ctx, &opts.Description, opts.io, opts.config, func(ctx context.Context) (string, error) {
		return opts.selectTemplate(ctx)
	})
	if err != nil {
		return err
	}

	if opts.needsPrompt {
		// Step 1: Template selection (if not using --no-editor and description is empty)
		if opts.Description == "" && !opts.noEditor {
			templateContents, err = opts.selectTemplate(ctx)
			if err != nil {
				return err
			}
		}

		// Step 2: Title and Description in a single form
		needsTitle := opts.Title == ""
		needsDescription := opts.Description == ""

		if needsTitle || needsDescription {
			var fields []huh.Field

			// Add title field if needed
			if needsTitle {
				fields = append(fields, huh.NewInput().
					Title("Title").
					Value(&opts.Title).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("title is required")
						}
						return nil
					}))
			}

			// Add description field if needed
			if needsDescription {
				if opts.noEditor {
					// Use multiline text input
					fields = append(fields, huh.NewText().
						Title("Description").
						Value(&opts.Description))
				} else {
					// Use editor with template contents
					editor, err := cmdutils.GetEditor(opts.config)
					if err != nil {
						return err
					}

					// Set initial value from template
					if templateContents != "" {
						opts.Description = templateContents
					}

					textField := huh.NewText().
						Title("Description").
						Value(&opts.Description).
						ExternalEditor(true).
						EditorExtension(".md")

					if editor != "" {
						// Parse editor command to handle arguments properly
						// huh.Editor() accepts variadic args: Editor(cmd, arg1, arg2, ...)
						editorParts := utils.ParseEditorCommand(editor)
						if len(editorParts) > 0 {
							textField = textField.Editor(editorParts...)
						}
					}

					fields = append(fields, textField)
				}
			}

			// Run the combined form
			if len(fields) > 0 {
				err = opts.io.RunForm(ctx, fields...)
				if err != nil {
					return err
				}
			}
		}
	} else if opts.Title == "" {
		return fmt.Errorf("title can't be blank")
	}

	var action cmdutils.Action

	// submit without prompting for non interactive mode
	if !opts.needsPrompt || opts.yes {
		action = cmdutils.SubmitAction
	}

	if opts.web {
		action = cmdutils.PreviewAction
	}

	if action == cmdutils.NoAction {
		action, err = cmdutils.ConfirmSubmission(ctx, opts.io, true)
		if err != nil {
			return fmt.Errorf("unable to confirm: %w", err)
		}
	}

	if action == cmdutils.AddMetadataAction {
		var metadataActions []cmdutils.Action

		metadataActions, err = cmdutils.PickMetadata(ctx, opts.io)
		if err != nil {
			return fmt.Errorf("failed to pick metadata to add: %w", err)
		}

		remotes, err := opts.remotes()
		if err != nil {
			return err
		}
		repoRemote, err := remotes.FindByRepo(repo.RepoOwner(), repo.RepoName())
		if err != nil {
			// when the base repo is overridden with --repo flag, it is likely it has no
			// remote set for the current working git dir which will error.
			// Init a new remote without actually adding a new git remote
			repoRemote = &glrepo.Remote{
				Repo: repo,
			}
		}

		for _, x := range metadataActions {
			if x == cmdutils.AddLabelAction {
				err = cmdutils.LabelsPrompt(ctx, opts.io, &opts.Labels, apiClient, repoRemote)
				if err != nil {
					return err
				}

			}
			if x == cmdutils.AddAssigneeAction {
				// Involve only reporters and up, in the future this might be expanded to `guests`
				// but that might hit the `100` limit for projects with large amounts of collaborators
				err = cmdutils.UsersPrompt(ctx, &opts.Assignees, apiClient, repoRemote, opts.io, 20, "assignees")
				if err != nil {
					return err
				}
			}
			if x == cmdutils.AddMilestoneAction {
				err = cmdutils.MilestonesPrompt(ctx, &opts.Milestone, apiClient, repoRemote, opts.io)
				if err != nil {
					return err
				}

			}
		}

		// Ask the user again but don't permit AddMetadata a second time
		action, err = cmdutils.ConfirmSubmission(ctx, opts.io, false)
		if err != nil {
			return err
		}
	}

	if action == cmdutils.CancelAction {
		fmt.Fprintln(opts.io.StdErr, "Discarded.")
		return nil
	}

	if action == cmdutils.PreviewAction {
		return previewIssue(opts)
	}

	if action == cmdutils.SubmitAction {
		issueCreateOpts.Title = new(opts.Title)
		issueCreateOpts.Labels = (*gitlab.LabelOptions)(&opts.Labels)
		issueCreateOpts.Description = &opts.Description
		if opts.IsConfidential {
			issueCreateOpts.Confidential = new(opts.IsConfidential)
		}
		if opts.Weight != 0 {
			issueCreateOpts.Weight = new(opts.Weight)
		}
		if opts.LinkedMR != 0 {
			issueCreateOpts.MergeRequestToResolveDiscussionsOf = new(opts.LinkedMR)
		}
		if opts.Milestone != 0 {
			issueCreateOpts.MilestoneID = new(opts.Milestone)
		}
		if opts.EpicID != 0 {
			issueCreateOpts.EpicID = new(opts.EpicID)
		}
		if opts.DueDate != "" {
			dueDate, err := gitlab.ParseISOTime(opts.DueDate)
			if err != nil {
				return err
			}
			issueCreateOpts.DueDate = new(dueDate)
		}

		if len(opts.Assignees) > 0 {
			users, err := api.UsersByNames(apiClient, opts.Assignees)
			if err != nil {
				return err
			}
			issueCreateOpts.AssigneeIDs = cmdutils.IDsFromUsers(users)
		}
		fmt.Fprintln(opts.io.StdErr, "- Creating issue in", repo.FullName())
		issue, err := createIssue(apiClient, repo.FullName(), issueCreateOpts)
		if err != nil {
			return err
		}
		if err := postCreateActions(apiClient, issue, opts, repo); err != nil {
			return err
		}

		fmt.Fprintln(opts.io.StdOut, issueutils.DisplayIssue(opts.io.Color(), issue, opts.io.IsaTTY))
		return nil
	}

	return errors.New("expected to cancel, preview in browser, add metadata, or submit")
}

func postCreateActions(apiClient *gitlab.Client, issue *gitlab.Issue, opts *options, repo glrepo.Interface) error {
	if len(opts.LinkedIssues) > 0 {
		for _, targetIssueIID := range opts.LinkedIssues {
			fmt.Fprintln(opts.io.StdErr, "- Linking to issue ", targetIssueIID)
			issueLink, _, err := apiClient.IssueLinks.CreateIssueLink(repo.FullName(), issue.IID, &gitlab.CreateIssueLinkOptions{
				TargetIssueIID: new(strconv.Itoa(targetIssueIID)),
				LinkType:       new(opts.IssueLinkType),
			})
			if err != nil {
				return err
			}
			issue = issueLink.SourceIssue
		}
	}
	if opts.TimeEstimate != "" {
		fmt.Fprintln(opts.io.StdErr, "- Adding time estimate ", opts.TimeEstimate)
		_, _, err := apiClient.Issues.SetTimeEstimate(repo.FullName(), issue.IID, &gitlab.SetTimeEstimateOptions{Duration: new(opts.TimeEstimate)})
		if err != nil {
			return err
		}
	}
	if opts.TimeSpent != "" {
		fmt.Fprintln(opts.io.StdErr, "- Adding time spent ", opts.TimeSpent)
		_, _, err := apiClient.Issues.AddSpentTime(repo.FullName(), issue.IID, &gitlab.AddSpentTimeOptions{Duration: new(opts.TimeSpent)})
		if err != nil {
			return err
		}
	}
	return nil
}

func previewIssue(opts *options) error {
	repo, err := opts.baseRepo()
	if err != nil {
		return err
	}

	cfg := opts.config()

	openURL, err := generateIssueWebURL(opts)
	if err != nil {
		return err
	}

	if opts.io.IsOutputTTY() {
		fmt.Fprintf(opts.io.StdErr, "Opening %s in your browser.\n", utils.DisplayURL(openURL))
	}
	browser, _ := cfg.Get(repo.RepoHost(), "browser")
	return utils.OpenInBrowser(openURL, browser)
}

func generateIssueWebURL(opts *options) (string, error) {
	var description strings.Builder
	description.WriteString(opts.Description)

	if len(opts.Labels) > 0 {
		// this uses the slash commands to add labels to the description
		// See https://docs.gitlab.com/user/project/quick_actions/
		// See also https://gitlab.com/gitlab-org/gitlab-foss/-/issues/19731#note_32550046
		description.WriteString("\n/label")
		for _, label := range opts.Labels {
			description.WriteString(fmt.Sprintf(" ~%q", label))
		}
	}
	if len(opts.Assignees) > 0 {
		// this uses the slash commands to add assignees to the description
		description.WriteString(fmt.Sprintf("\n/assign %s", strings.Join(opts.Assignees, ", ")))
	}
	if opts.Milestone != 0 {
		// this uses the slash commands to add milestone to the description
		description.WriteString(fmt.Sprintf("\n/milestone %%%d", opts.Milestone))
	}
	if opts.Weight != 0 {
		// this uses the slash commands to add weight to the description
		description.WriteString(fmt.Sprintf("\n/weight %d", opts.Weight))
	}
	if opts.IsConfidential {
		// this uses the slash commands to add confidential to the description
		description.WriteString("\n/confidential")
	}

	u, err := url.Parse(opts.baseProject.WebURL)
	if err != nil {
		return "", err
	}
	u.Path += "/-/issues/new"

	q := u.Query()
	q.Set("issue[title]", opts.Title)
	q.Add("issue[description]", description.String())
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// createRecoverSaveFile will try save the issue create options to a file
func createRecoverSaveFile(repoName string, opts *options) error {
	recoverFile, err := recovery.CreateFile(repoName, "issue.json", opts)
	if err != nil {
		return err
	}

	fmt.Fprintf(opts.io.StdErr, "Failed to create issue. Created recovery file: %s\nRun the command again with the '--recover' option to retry", recoverFile)
	return nil
}
