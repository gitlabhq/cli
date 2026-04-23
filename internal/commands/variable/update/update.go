package update

import (
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/variable/variableutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	key         string
	value       string
	typ         string
	scope       string
	protected   bool
	masked      bool
	raw         bool
	group       string
	description string
}

func NewCmdUpdate(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "update <key> <value>",
		Short: "Update an existing variable for a project or group.",
		Args:  cobra.RangeArgs(1, 2),
		Example: heredoc.Doc(`
			glab variable update WITH_ARG "some value"
			glab variable update FROM_FLAG -v "some value"
			glab variable update FROM_ENV_WITH_ARG "${ENV_VAR}"
			glab variable update FROM_ENV_WITH_FLAG -v"${ENV_VAR}"
			glab variable update FROM_FILE < secret.txt
			cat file.txt | glab variable update SERVER_TOKEN
			cat token.txt | glab variable update GROUP_TOKEN -g mygroup --scope=prod`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(cmd, args); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}

			return opts.run()
		},
	}

	cmd.Flags().StringVarP(&opts.value, "value", "v", "", "The value of a variable.")
	cmd.Flags().StringVarP(&opts.typ, "type", "t", "env_var", "The type of a variable: env_var, file.")
	cmd.Flags().StringVarP(&opts.scope, "scope", "s", "*", "The environment_scope of the variable. Values: all (*), or specific environments.")
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Set variable for a group.")
	cmd.Flags().BoolVarP(&opts.masked, "masked", "m", false, "Whether the variable is masked. (default false)")
	cmd.Flags().BoolVarP(&opts.raw, "raw", "r", false, "Whether the variable is treated as a raw string. (default false)")
	cmd.Flags().BoolVarP(&opts.protected, "protected", "p", false, "Whether the variable is protected. (default false)")
	cmd.Flags().StringVarP(&opts.description, "description", "d", "", "Set description of a variable.")
	return cmd
}

func (o *options) complete(args []string) {
	o.key = args[0]
}

func (o *options) validate(cmd *cobra.Command, args []string) error {
	if !variableutils.IsValidKey(o.key) {
		return cmdutils.FlagError{Err: fmt.Errorf("invalid key provided.\n%s", variableutils.ValidKeyMsg)}
	}

	if o.value != "" && len(args) == 2 {
		return cmdutils.FlagError{Err: errors.New("specify value either by the second positional argument or the --value flag.")}
	}

	if cmd.Flags().Changed("scope") && o.group != "" {
		return cmdutils.FlagError{Err: errors.New("scope is not required for group variables.")}
	}

	value, err := variableutils.GetValue(o.value, o.io, args)
	if err != nil {
		return err
	}
	o.value = value

	if cmd.Flags().Changed("type") {
		if o.typ != "env_var" && o.typ != "file" {
			return cmdutils.FlagError{Err: fmt.Errorf("invalid type: %s. --type must be one of `env_var` or `file`.", o.typ)}
		}
	}

	return nil
}

func (o *options) run() error {
	c := o.io.Color()

	// NOTE: this command can not only be used for projects,
	// so we have to manually check for the base repo, if it doesn't exist,
	// we bootstrap the client with the default hostname.
	var repoHost string
	if baseRepo, err := o.baseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	if o.group != "" {
		// update group-level variable
		updateGroupVarOpts := &gitlab.UpdateGroupVariableOptions{
			Value:            new(o.value),
			VariableType:     new(gitlab.VariableTypeValue(o.typ)),
			Masked:           new(o.masked),
			Protected:        new(o.protected),
			Raw:              new(o.raw),
			EnvironmentScope: new(o.scope),
			Description:      new(o.description),
		}

		_, _, err = client.GroupVariables.UpdateVariable(o.group, o.key, updateGroupVarOpts)
		if err != nil {
			return err
		}

		fmt.Fprintf(o.io.StdOut, "%s Updated variable %s for group %s.\n", c.GreenCheck(), o.key, o.group)
		return nil
	}

	// update project-level variable
	baseRepo, err := o.baseRepo()
	if err != nil {
		return err
	}

	updateProjectVarOpts := &gitlab.UpdateProjectVariableOptions{
		Value:            new(o.value),
		VariableType:     new(gitlab.VariableTypeValue(o.typ)),
		Masked:           new(o.masked),
		Protected:        new(o.protected),
		Raw:              new(o.raw),
		EnvironmentScope: new(o.scope),
		Filter:           &gitlab.VariableFilter{EnvironmentScope: o.scope},
		Description:      new(o.description),
	}
	_, _, err = client.ProjectVariables.UpdateVariable(baseRepo.FullName(), o.key, updateProjectVarOpts)
	if err != nil {
		return err
	}

	fmt.Fprintf(o.io.StdOut, "%s Updated variable %s for project %s with scope %s.\n", c.GreenCheck(), o.key, baseRepo.FullName(), o.scope)
	return nil
}
