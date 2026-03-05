package get

import (
	"encoding/json"
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

	scope        string
	key          string
	group        string
	outputFormat string
}

func NewCmdGet(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a variable for a project or group.",
		Args:  cobra.RangeArgs(1, 1),
		Example: heredoc.Doc(`
			$ glab variable get VAR_KEY
			$ glab variable get -g GROUP VAR_KEY
			$ glab variable get -s SCOPE VAR_KEY
		`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}
			return opts.run()
		},
	}

	cmd.Flags().StringVarP(&opts.scope, "scope", "s", "*", "The environment_scope of the variable. Values: all (*), or specific environments.")
	cmd.Flags().StringVarP(&opts.group, "group", "g", "", "Get variable for a group.")
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat)
	return cmd
}

func (o *options) complete(args []string) {
	o.key = args[0]
}

func (o *options) validate() error {
	if !variableutils.IsValidKey(o.key) {
		return cmdutils.FlagError{Err: fmt.Errorf("invalid key provided.\n%s", variableutils.ValidKeyMsg)}
	}

	return nil
}

func (o *options) run() error {
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

	var variableValue string

	if o.group != "" {
		variable, _, err := client.GroupVariables.GetVariable(o.group, o.key, &gitlab.GetGroupVariableOptions{
			Filter: &gitlab.VariableFilter{EnvironmentScope: o.scope},
		})
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			varJSON, _ := json.Marshal(variable)
			fmt.Println(string(varJSON))
		}
		variableValue = variable.Value
	} else {
		baseRepo, err := o.baseRepo()
		if err != nil {
			return err
		}

		variable, _, err := client.ProjectVariables.GetVariable(baseRepo.FullName(), o.key, &gitlab.GetProjectVariableOptions{
			Filter: &gitlab.VariableFilter{EnvironmentScope: o.scope},
		})
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			varJSON, _ := json.Marshal(variable)
			fmt.Fprintln(o.io.StdOut, string(varJSON))
		}
		variableValue = variable.Value
	}

	if o.outputFormat != "json" {
		fmt.Fprint(o.io.StdOut, variableValue)
	}
	return nil
}
