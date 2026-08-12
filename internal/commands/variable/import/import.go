package importcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

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

// importedVariable holds the fields shared by project and group variables, as
// emitted by `glab variable export --output json`. Importing reads this back.
type importedVariable struct {
	Key              string `json:"key"`
	Value            string `json:"value"`
	VariableType     string `json:"variable_type"`
	Protected        bool   `json:"protected"`
	Masked           bool   `json:"masked"`
	Hidden           bool   `json:"hidden"`
	Raw              bool   `json:"raw"`
	EnvironmentScope string `json:"environment_scope"`
	Description      string `json:"description"`
}

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	group        string
	inputFile    string
	skipExisting bool
}

func NewCmd(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import variables from a JSON file or standard input.",
		Long: heredoc.Docf(`
			The inverse of %[1]sglab variable export%[1]s. Reads a JSON array of
			variable objects, in the same shape %[1]sexport --output json%[1]s emits.

			The command:

			- Reads from standard input, or from a file with %[1]s--input-file%[1]s.
			- Imports into the current project by default. Use %[1]s--group%[1]s to
			  import into a group instead.
			- Stops with an error if a variable already exists. Pass
			  %[1]s--skip-existing%[1]s to skip those and continue.

			Hidden variables' values aren't included in %[1]sexport%[1]s output,
			so re-importing one is skipped with a warning. Set its value again
			with %[1]sglab variable set --hidden%[1]s.
		`, "`"),
		Aliases: []string{"im"},
		Args:    cobra.NoArgs,
		Example: heredoc.Doc(`
			# Pipe an export straight into an import, to restore the same project
			glab variable export | glab variable import

			# Import variables from a saved file instead of standard input
			glab variable import --input-file variables.json

			# Import into a group instead of the current project
			glab variable export --group gitlab-org | glab variable import --group gitlab-org

			# Skip variables that already exist instead of failing
			glab variable import --input-file variables.json --skip-existing`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.PersistentFlags().StringP("group", "g", "", "Select a group or subgroup. Ignored if a repository argument is set.")

	fl := cmd.Flags()
	fl.StringVarP(&opts.inputFile, "input-file", "i", "", "Read the variables JSON from this file instead of standard input.")
	fl.BoolVar(&opts.skipExisting, "skip-existing", false, "Skip variables that already exist instead of failing.")
	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func (o *options) read() ([]importedVariable, error) {
	var (
		data []byte
		err  error
	)
	if o.inputFile != "" {
		data, err = os.ReadFile(o.inputFile)
	} else {
		data, err = io.ReadAll(o.io.In)
	}
	if err != nil {
		return nil, err
	}

	var variables []importedVariable
	if err := json.Unmarshal(data, &variables); err != nil {
		return nil, fmt.Errorf("parsing variables JSON: %w", err)
	}
	return variables, nil
}

// validate checks every key up front and defaults an empty EnvironmentScope to
// "*", so a bad entry is rejected before any variable has been created —
// otherwise a later invalid key would leave a partial import behind.
func validate(variables []importedVariable) error {
	for i, v := range variables {
		if !variableutils.IsValidKey(v.Key) {
			return fmt.Errorf("invalid key %q: %s", v.Key, variableutils.ValidKeyMsg)
		}
		if v.EnvironmentScope == "" {
			variables[i].EnvironmentScope = "*"
		}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	c := o.io.Color()

	variables, err := o.read()
	if err != nil {
		return err
	}
	if len(variables) == 0 {
		o.io.LogInfo("No variables to import.")
		return nil
	}
	if err := validate(variables); err != nil {
		return err
	}

	// NOTE: this command can not only be used for projects, so we have to
	// manually check for the base repo. If it doesn't exist, we bootstrap the
	// client with the default hostname.
	var repoHost string
	if baseRepo, err := o.baseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	target := o.group
	if target == "" {
		baseRepo, err := o.baseRepo()
		if err != nil {
			return err
		}
		target = baseRepo.FullName()
	}

	created, skipped := 0, 0
	for _, v := range variables {
		if v.Hidden && v.Value == "" {
			o.io.LogInfof("%s Skipped %s: hidden variables' values aren't included in `variable export`; set it again with `glab variable set --hidden`.\n", c.WarnIcon(), v.Key)
			skipped++
			continue
		}

		if err := o.create(ctx, client, target, v); err != nil {
			if o.skipExisting && isAlreadyExists(err) {
				o.io.LogInfof("%s Skipped existing variable %s.\n", c.WarnIcon(), v.Key)
				skipped++
				continue
			}
			return fmt.Errorf("importing variable %s: %w", v.Key, err)
		}
		o.io.LogInfof("%s Imported variable %s.\n", c.GreenCheck(), v.Key)
		created++
	}

	summary := fmt.Sprintf("Imported %d variables into %s", created, target)
	if skipped > 0 {
		summary += fmt.Sprintf(" (%d skipped)", skipped)
	}
	o.io.LogInfo(summary + ".")
	return nil
}

func (o *options) create(ctx context.Context, client *gitlab.Client, target string, v importedVariable) error {
	if o.group != "" {
		groupOpts := &gitlab.CreateGroupVariableOptions{
			Key:              new(v.Key),
			Value:            new(v.Value),
			EnvironmentScope: new(v.EnvironmentScope),
			Masked:           new(v.Masked),
			Protected:        new(v.Protected),
			VariableType:     new(gitlab.VariableTypeValue(v.VariableType)),
			Raw:              new(v.Raw),
			Description:      new(v.Description),
		}
		if v.Hidden {
			groupOpts.MaskedAndHidden = new(true)
		}
		_, _, err := client.GroupVariables.CreateVariable(target, groupOpts, gitlab.WithContext(ctx))
		return err
	}

	projectOpts := &gitlab.CreateProjectVariableOptions{
		Key:              new(v.Key),
		Value:            new(v.Value),
		EnvironmentScope: new(v.EnvironmentScope),
		Masked:           new(v.Masked),
		Protected:        new(v.Protected),
		VariableType:     new(gitlab.VariableTypeValue(v.VariableType)),
		Raw:              new(v.Raw),
		Description:      new(v.Description),
	}
	if v.Hidden {
		projectOpts.MaskedAndHidden = new(true)
	}
	_, _, err := client.ProjectVariables.CreateVariable(target, projectOpts, gitlab.WithContext(ctx))
	return err
}

// isAlreadyExists reports whether err is GitLab's "key has already been
// taken" validation error, returned when a variable with the same key and
// scope exists. It inspects the structured API error (status + message)
// rather than matching on err.Error(), which also bakes in the request
// method and URL.
func isAlreadyExists(err error) bool {
	var errResp *gitlab.ErrorResponse
	if !errors.As(err, &errResp) || !errResp.HasStatusCode(http.StatusBadRequest) {
		return false
	}
	return strings.Contains(strings.ToLower(errResp.Message), "has already been taken")
}
