package view

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/container_registry/registryutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	repositoryID     int64
	includeTags      bool
	includeTagsCount bool
	outputFormat     string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "view <repository-id> [flags]",
		Short: "View a container registry repository.",
		Long: heredoc.Docf(`
			Returns the container registry repository's details. Use %[1]s--include-tags%[1]s to include its
			tags in the output.
		`, "`"),
		Aliases: []string{"show"},
		Args:    cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# View a container registry repository
			glab container-registry repository view 123

			# Include tag details
			glab container-registry repository view 123 --include-tags`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run()
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&opts.includeTags, "include-tags", false, "Include tags in the response.")
	fl.BoolVar(&opts.includeTagsCount, "include-tags-count", true, "Include the number of tags in the response.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) complete(args []string) error {
	repositoryID, err := registryutils.ParseID(args[0], "repository ID")
	if err != nil {
		return &cmdutils.FlagError{Err: err}
	}
	o.repositoryID = repositoryID

	return nil
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	opts := &gitlab.GetSingleRegistryRepositoryOptions{}
	if o.includeTags {
		opts.Tags = new(true)
	}
	if o.includeTagsCount {
		opts.TagsCount = new(true)
	}

	repository, _, err := client.ContainerRegistry.GetSingleRegistryRepository(o.repositoryID, opts)
	if err != nil {
		return cmdutils.WrapError(err, fmt.Sprintf("failed to fetch container registry repository %d.", o.repositoryID))
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(registryutils.NewRepositoryJSON(repository, false, o.includeTagsCount))
	}

	o.io.LogInfo(registryutils.DisplayRepository(o.io, repository))
	return nil
}
