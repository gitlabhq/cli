package list

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepoFunc func() (glrepo.Interface, error)

	agentID      int64
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepoFunc: f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "list <agent-id> [flags]",
		Short: `List tokens for a GitLab Agent for Kubernetes.`,
		Long: heredoc.Doc(`
			Lists all tokens for the specified agent. Requires the numeric agent ID.
		`),
		Example: heredoc.Doc(`
			# List tokens for agent 123
			glab cluster agent token list 123

			# List tokens in JSON format
			glab cluster agent token list 123 --output json`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) complete(args []string) error {
	agentID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("agent ID must be a valid integer, got %q", args[0])
	}
	o.agentID = agentID

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	baseRepo, err := o.baseRepoFunc()
	if err != nil {
		return err
	}

	tokens, _, err := client.ClusterAgents.ListAgentTokens(baseRepo.FullName(), o.agentID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("unable to retrieve agent tokens: %w", err)
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(tokens)
	}

	c := o.io.Color()
	bold := c.Bold

	table := tableprinter.NewTablePrinter()
	table.AddRow(bold("ID"), bold("Name"), bold("Status"), bold("Created At"), bold("Created By"), bold("Last Used At"), bold("Description"))
	var username string
	// NOTE: there can only ever be two tokens registered for an agent at once, therefore, it's safe to assume that
	// we only ever get a maximum of two items back from the API, despite it's slice return type.
	var cachedUserID int64
	for _, token := range tokens {
		var lastUsedAt string
		switch token.LastUsedAt {
		case nil:
			lastUsedAt = c.Gray("never")
		default:
			lastUsedAt = token.LastUsedAt.Format(time.RFC3339)
		}

		if cachedUserID != token.CreatedByUserID {
			user, _, err := client.Users.GetUser(token.CreatedByUserID, &gitlab.GetUserOptions{}, gitlab.WithContext(ctx))
			if err != nil {
				username = fmt.Sprintf("%d", token.CreatedByUserID)
			} else {
				username = user.Username
			}
			cachedUserID = token.CreatedByUserID
		}

		table.AddRow(token.ID, token.Name, token.Status, token.CreatedAt.Format(time.RFC3339), username, lastUsedAt, token.Description)
	}
	o.io.LogInfof("%s", table.Render())

	return nil
}
