package export

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)

	group        string
	outputFormat string
	scope        string

	page    int
	perPage int
}

func NewCmdExport(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export variables from a project or group.",
		Long: heredoc.Docf(`
			Defaults to the current project. Use %[1]s--group%[1]s to export
			variables for a group. Use %[1]s--output%[1]s to set the format:
			%[1]sjson%[1]s (default), %[1]senv%[1]s (KEY=VALUE pairs), or
			%[1]sexport%[1]s (shell export statements).
		`, "`"),
		Aliases: []string{"ex"},
		Args:    cobra.NoArgs,
		Example: heredoc.Doc(`
			glab variable export
			glab variable export --per-page 1000 --page 1
			glab variable export --group gitlab-org
			glab variable export --group gitlab-org --per-page 1000 --page 1
			glab variable export --output json
			glab variable export --output env
			glab variable export --output export`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}

			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.PersistentFlags().StringP("group", "g", "", "Select a group or subgroup. Ignored if a repository argument is set.")

	fl := cmd.Flags()
	fl.IntVarP(&opts.page, "page", "p", 1, "Page number.")
	fl.IntVarP(&opts.perPage, "per-page", "P", 100, "Number of items to list per page.")
	fl.StringVarP(&opts.outputFormat, "output", "F", "json", "Format output as: json, export, env.")
	fl.StringVarP(&opts.scope, "scope", "s", "*", "The environment_scope of the variables. Values: '*' (default), or specific environments.")

	// Deprecated: --format flag, use --output instead
	fl.StringVar(&opts.outputFormat, "format", "json", "Format of output: json, export, env.")
	_ = fl.MarkDeprecated("format", "use --output instead.")

	cmdutils.AddJQFlag(cmd, f.IO())
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

func (o *options) run() error {
	var out io.Writer = os.Stdout
	if o.io != nil && o.io.StdOut != nil {
		out = o.io.StdOut
	}

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
		o.io.LogErrorf("Exporting variables from the %s group:\n", o.group)
		createVarOpts := &gitlab.ListGroupVariablesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		}
		groupVariables, _, err := client.GroupVariables.ListVariables(o.group, createVarOpts)
		if err != nil {
			return err
		}

		if len(groupVariables) == 0 {
			return nil
		}

		return printGroupVariables(groupVariables, o, out)

	} else {
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}
		o.io.LogErrorf("Exporting variables from the %s project:\n", repo.FullName())
		listOpts := &gitlab.ListProjectVariablesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		}
		projectVariables, _, err := client.ProjectVariables.ListVariables(repo.FullName(), listOpts)
		if err != nil {
			return err
		}

		if len(projectVariables) == 0 {
			return nil
		}

		return printProjectVariables(projectVariables, o, out)
	}
}

func matchesScope(varScope, optScope string) bool {
	if varScope == "*" || optScope == "*" {
		return true
	}
	if varScope == optScope {
		return true
	}
	if strings.Contains(varScope, "*") {
		varPattern := "^" + regexp.QuoteMeta(varScope) + "$"
		optPattern := "^" + regexp.QuoteMeta(optScope) + "$"

		varPattern = strings.ReplaceAll(varPattern, `\*`, ".*")
		optPattern = strings.ReplaceAll(optPattern, `\*`, ".*")

		matchesVar, _ := regexp.MatchString(varPattern, optScope)
		matchesOpt, _ := regexp.MatchString(optPattern, varScope)

		return matchesVar || matchesOpt
	}
	return false
}

func isValidEnvironmentScope(optScope string) bool {
	pattern := `^[a-zA-Z0-9\s\-_/${}\x20]+$`
	re, _ := regexp.Compile(pattern)
	matched := re.MatchString(optScope)
	return matched || optScope == "*"
}

func printGroupVariables(variables []*gitlab.GroupVariable, opts *options, out io.Writer) error {
	if !isValidEnvironmentScope((opts.scope)) {
		return fmt.Errorf("invalid environment scope: %s", opts.scope)
	}

	writtenKeys := make([]string, 0)
	switch opts.outputFormat {
	case "env":
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !strings.Contains(variable.EnvironmentScope, "*") {
					fmt.Fprintf(out, "%s=%s\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
					writtenKeys = append(writtenKeys, variable.Key)
				}
			}
		}
		keysMap := CreateWrittenKeysMap(writtenKeys)
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !(keysMap[variable.Key]) && (strings.Contains(variable.EnvironmentScope, "*")) {
					fmt.Fprintf(out, "%s=%s\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
				}
			}
		}
	case "export":
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !strings.Contains(variable.EnvironmentScope, "*") {
					fmt.Fprintf(out, "export %s=%s\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
					writtenKeys = append(writtenKeys, variable.Key)
				}
			}
		}
		keysMap := CreateWrittenKeysMap(writtenKeys)
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !(keysMap[variable.Key]) && (strings.Contains(variable.EnvironmentScope, "*")) {
					fmt.Fprintf(out, "export %s=%s\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
				}
			}
		}
	case "json":
		filteredVariables := make([]*gitlab.GroupVariable, 0)
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				filteredVariables = append(filteredVariables, variable)
			}
		}
		return opts.io.PrintJSON(filteredVariables)
	default:
		return fmt.Errorf("unsupported output format: %s", opts.outputFormat)
	}

	return nil
}

func printProjectVariables(variables []*gitlab.ProjectVariable, opts *options, out io.Writer) error {
	if !isValidEnvironmentScope((opts.scope)) {
		return fmt.Errorf("invalid environment scope: %s", opts.scope)
	}

	writtenKeys := make([]string, 0)
	switch opts.outputFormat {
	case "env":
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !strings.Contains(variable.EnvironmentScope, "*") {
					fmt.Fprintf(out, "%s=\"%s\"\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
					writtenKeys = append(writtenKeys, variable.Key)
				}
			}
		}
		keysMap := CreateWrittenKeysMap(writtenKeys)
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !(keysMap[variable.Key]) && (strings.Contains(variable.EnvironmentScope, "*")) {
					fmt.Fprintf(out, "%s=\"%s\"\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
				}
			}
		}
	case "export":
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !strings.Contains(variable.EnvironmentScope, "*") {
					fmt.Fprintf(out, "export %s=\"%s\"\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
					writtenKeys = append(writtenKeys, variable.Key)
				}
			}
		}
		keysMap := CreateWrittenKeysMap(writtenKeys)
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				if !(keysMap[variable.Key]) && (strings.Contains(variable.EnvironmentScope, "*")) {
					fmt.Fprintf(out, "export %s=\"%s\"\n", variable.Key, variable.Value) //nolint:forbidigo // out is a generic io.Writer with an os.Stdout fallback, not IOStreams directly
				}
			}
		}
	case "json":
		filteredVariables := make([]*gitlab.ProjectVariable, 0)
		for _, variable := range variables {
			if matchesScope(variable.EnvironmentScope, opts.scope) {
				filteredVariables = append(filteredVariables, variable)
			}
		}
		return opts.io.PrintJSON(filteredVariables)
	default:
		return fmt.Errorf("unsupported output format: %s", opts.outputFormat)
	}

	return nil
}

func CreateWrittenKeysMap(writtenKeys []string) map[string]bool {
	keysMap := make(map[string]bool)
	for _, key := range writtenKeys {
		keysMap[key] = true
	}
	return keysMap
}
