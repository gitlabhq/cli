package update

import (
	"context"
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

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

	// Attributes that were not passed on the command line are left out of the
	// request, so the API keeps their current values.
	typeChanged        bool
	protectedChanged   bool
	maskedChanged      bool
	rawChanged         bool
	descriptionChanged bool
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
		Long: heredoc.Docf(`
			You can pass the variable value from standard input. Use %[1]s--group%[1]s
			to update a variable for a group instead of the current project.

			Use %[1]s--scope%[1]s to select the environment scope of the project or
			group variable. The default scope is %[1]s*%[1]s. Environment scopes for
			group variables require GitLab Premium or Ultimate.

			Only the attributes you pass are changed. For example, %[1]s--protected%[1]s,
			%[1]s--masked%[1]s, %[1]s--raw%[1]s, %[1]s--type%[1]s and %[1]s--description%[1]s keep their
			current values unless you set them. To turn a boolean attribute off, use
			%[1]s--protected=false%[1]s.
		`, "`"),
		Args: cobra.RangeArgs(1, 2),
		Example: heredoc.Doc(`
			glab variable update WITH_ARG "some value"
			glab variable update FROM_FLAG -v "some value"
			glab variable update FROM_ENV_WITH_ARG "${ENV_VAR}"
			glab variable update FROM_ENV_WITH_FLAG -v"${ENV_VAR}"
			glab variable update FROM_FILE < secret.txt
			cat file.txt | glab variable update SERVER_TOKEN
			cat token.txt | glab variable update GROUP_TOKEN -g mygroup

			# Update a group variable in the production environment.
			glab variable update GROUP_TOKEN -g mygroup --scope prod --value "some value"`),
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

			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVarP(&opts.value, "value", "v", "", "The value of a variable.")
	cmd.Flags().StringVarP(&opts.typ, "type", "t", "env_var", "The type of a variable: env_var, file.")
	cmd.Flags().StringVarP(&opts.scope, "scope", "s", "*", "The environment_scope of the variable. Values: all (*), or specific environments.")
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Set variable for a group.")
	cmd.Flags().BoolVarP(&opts.masked, "masked", "m", false, "Whether the variable is masked.")
	cmd.Flags().BoolVarP(&opts.raw, "raw", "r", false, "Whether the variable is treated as a raw string.")
	cmd.Flags().BoolVarP(&opts.protected, "protected", "p", false, "Whether the variable is protected.")
	cmd.Flags().StringVarP(&opts.description, "description", "d", "", "Set description of a variable.")
	return cmd
}

func (o *options) complete(args []string) {
	o.key = args[0]
}

func (o *options) validate(cmd *cobra.Command, args []string) error {
	o.typeChanged = cmd.Flags().Changed("type")
	o.protectedChanged = cmd.Flags().Changed("protected")
	o.maskedChanged = cmd.Flags().Changed("masked")
	o.rawChanged = cmd.Flags().Changed("raw")
	o.descriptionChanged = cmd.Flags().Changed("description")

	if !variableutils.IsValidKey(o.key) {
		return cmdutils.FlagError{Err: fmt.Errorf("invalid key provided: %s", variableutils.ValidKeyMsg)}
	}

	if o.value != "" && len(args) == 2 {
		return cmdutils.FlagError{Err: errors.New("specify value either by the second positional argument or the --value flag")}
	}

	value, err := variableutils.GetValue(o.value, o.io, args)
	if err != nil {
		return err
	}
	o.value = value

	if cmd.Flags().Changed("type") {
		if o.typ != "env_var" && o.typ != "file" {
			return cmdutils.FlagError{Err: fmt.Errorf("invalid type %q: --type must be one of `env_var` or `file`", o.typ)}
		}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
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
		// GitLab can update another scope when the requested group variable is absent.
		// Confirm the variable belongs to the exact scope before changing it.
		variable, _, err := client.GroupVariables.GetVariable(o.group, o.key, &gitlab.GetGroupVariableOptions{
			Filter: &gitlab.VariableFilter{EnvironmentScope: o.scope},
		}, gitlab.WithContext(ctx))
		if err != nil {
			return err
		}
		if variable == nil || variable.EnvironmentScope != o.scope {
			return fmt.Errorf("group variable %q does not exist in scope %q", o.key, o.scope)
		}

		// update group-level variable
		_, _, err = client.GroupVariables.UpdateVariable(o.group, o.key, o.groupUpdateOptions(), gitlab.WithContext(ctx))
		if err != nil {
			return err
		}

		o.io.LogInfof("%s Updated variable %s for group %s with scope %s.\n", c.GreenCheck(), o.key, o.group, o.scope)
		return nil
	}

	// update project-level variable
	baseRepo, err := o.baseRepo()
	if err != nil {
		return err
	}

	_, _, err = client.ProjectVariables.UpdateVariable(baseRepo.FullName(), o.key, o.projectUpdateOptions(), gitlab.WithContext(ctx))
	if err != nil {
		return err
	}

	o.io.LogInfof("%s Updated variable %s for project %s with scope %s.\n", c.GreenCheck(), o.key, baseRepo.FullName(), o.scope)
	return nil
}

func (o *options) groupUpdateOptions() *gitlab.UpdateGroupVariableOptions {
	opts := &gitlab.UpdateGroupVariableOptions{
		Value:            new(o.value),
		EnvironmentScope: new(o.scope),
		Filter:           &gitlab.VariableFilter{EnvironmentScope: o.scope},
	}
	if o.typeChanged {
		opts.VariableType = new(gitlab.VariableTypeValue(o.typ))
	}
	if o.maskedChanged {
		opts.Masked = new(o.masked)
	}
	if o.protectedChanged {
		opts.Protected = new(o.protected)
	}
	if o.rawChanged {
		opts.Raw = new(o.raw)
	}
	if o.descriptionChanged {
		opts.Description = new(o.description)
	}
	return opts
}

func (o *options) projectUpdateOptions() *gitlab.UpdateProjectVariableOptions {
	opts := &gitlab.UpdateProjectVariableOptions{
		Value:            new(o.value),
		EnvironmentScope: new(o.scope),
		Filter:           &gitlab.VariableFilter{EnvironmentScope: o.scope},
	}
	if o.typeChanged {
		opts.VariableType = new(gitlab.VariableTypeValue(o.typ))
	}
	if o.maskedChanged {
		opts.Masked = new(o.masked)
	}
	if o.protectedChanged {
		opts.Protected = new(o.protected)
	}
	if o.rawChanged {
		opts.Raw = new(o.raw)
	}
	if o.descriptionChanged {
		opts.Description = new(o.description)
	}
	return opts
}
