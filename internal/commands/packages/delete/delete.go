package delete

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/packages/helpers"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	forceDelete    bool
	packageID      int64
	packageName    string
	packageVersion string
	packageType    string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return newCmd(f, "delete [<id> | --id <id> | --name <name>] [flags]", []string{"del"}, "")
}

// NewRmCmd returns the deprecated `rm` spelling of delete, kept so existing
// scripts keep working. Cobra prints the deprecation notice on every
// invocation; the command is hidden from help (including fang's renderer,
// which unlike cobra does not hide Deprecated commands), completion, and
// generated docs, and excluded from MCP tool registration, so the alias is
// no longer advertised anywhere.
//
// Cobra has no way to deprecate an alias, so this twin command is how the
// `rm` spelling keeps working while `delete` advertises `del` alone.
func NewRmCmd(f cmdutils.Factory) *cobra.Command {
	return newCmd(f, "rm [<id> | --id <id> | --name <name>] [flags]", nil, "use `glab packages del` instead.")
}

func newCmd(f cmdutils.Factory, use string, aliases []string, deprecated string) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	annotations := map[string]string{
		mcpannotations.Destructive: "true",
	}
	if deprecated != "" {
		// MCP tool registration walks every command regardless of
		// Hidden/Deprecated, so a deprecated spelling must opt out explicitly
		// or it surfaces as a duplicate tool next to the real command.
		annotations[mcpannotations.Exclude] = "true"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: `Delete a package from a project's package registry.`,
		Long: heredoc.Docf(`
		Identify the package by its numeric ID (as a positional argument or with
		%[1]s--id%[1]s), or by %[1]s--name%[1]s and %[1]s--version%[1]s. When using %[1]s--name%[1]s,
		%[1]s--package-type%[1]s defaults to %[1]sgeneric%[1]s.

		The command asks for confirmation before deleting; use %[1]s-y%[1]s to skip
		the prompt in scripts.

		By default, the package is removed from the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Aliases:    aliases,
		Deprecated: deprecated,
		Hidden:     deprecated != "",
		Args:       cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Delete a package by ID
			glab packages delete 1
			glab packages delete --id 1

			# Delete a generic package by name and version
			glab packages delete --name example-generic --version 1.0.0

			# Delete a package by name, version and type
			glab packages delete --name example-maven --version 1.0.0 --package-type maven

			# Skip the confirmation prompt
			glab packages delete 1 -y
			glab packages delete --name example-generic --version 1.0.0 --yes
		`),
		Annotations: annotations,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().BoolVarP(&opts.forceDelete, "yes", "y", false, "Skip the confirmation prompt.")
	cmd.Flags().Int64("id", 0, "ID of the package to remove.")
	cmd.Flags().String("name", "", "Name of the package to remove.")
	cmd.Flags().String("version", "", "Version of the package.")

	cmd.Flags().Var(
		cmdutils.NewEnumValue(helpers.PackageTypes, helpers.DefaultPackageType, &opts.packageType),
		"package-type",
		fmt.Sprintf("Type of the package to remove, used with --name. One of: %s.", strings.Join(helpers.PackageTypes, ", ")),
	)

	cmd.MarkFlagsMutuallyExclusive("id", "name")
	cmd.MarkFlagsRequiredTogether("name", "version")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	// A positional ID and the --id/--name flags select the package in conflicting
	// ways. Rejecting the combination avoids silently ignoring the argument and
	// removing a different package than the one named on the command line.
	if len(args) > 0 && (cmd.Flags().Changed("id") || cmd.Flags().Changed("name")) {
		return &cmdutils.FlagError{
			Err: fmt.Errorf("the package ID argument %q cannot be combined with --id or --name", args[0]),
		}
	}

	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("unable to get name flag: %w", err)
	}

	version, err := cmd.Flags().GetString("version")
	if err != nil {
		return fmt.Errorf("unable to get version flag: %w", err)
	}

	if name != "" {
		o.packageName = name
		o.packageVersion = version

		return nil
	}

	if cmd.Flags().Changed("package-type") {
		return &cmdutils.FlagError{Err: fmt.Errorf("--package-type can only be used with --name")}
	}

	if cmd.Flags().Changed("id") {
		o.packageID, err = cmd.Flags().GetInt64("id")
		if err != nil {
			return fmt.Errorf("unable to get id flag: %w", err)
		}
	} else {
		if len(args) == 0 {
			return &cmdutils.FlagError{Err: fmt.Errorf("provide a package ID argument, or the --id or --name flag")}
		}
		o.packageID, err = strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("package ID must be an integer: %s", args[0])
		}
	}

	return nil
}

func (o *options) validate() error {
	if !o.forceDelete && !o.io.PromptEnabled() {
		return &cmdutils.FlagError{Err: fmt.Errorf("--yes or -y flag is required when not running interactively")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	target := fmt.Sprintf("package %d", o.packageID)
	if o.packageName != "" {
		pkg, err := helpers.GetPackageByName(ctx, client, o.packageName, o.packageType, o.packageVersion, repo.FullName())
		if err != nil {
			return err
		}

		o.packageID = pkg.ID
		target = fmt.Sprintf("package %d (%s %s, type %s)", pkg.ID, pkg.Name, pkg.Version, pkg.PackageType)
	}

	confirmed := o.forceDelete
	if !confirmed && o.io.PromptEnabled() {
		o.io.LogInfof("This action will permanently delete %s immediately.\n\n", target)
		if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete this %s?", target)); err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !confirmed {
		return cmdutils.CancelError()
	}

	color := o.io.Color()
	o.io.LogInfof("%s Deleting package %s=%s %s=%d\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("packageID"), o.packageID)

	_, err = client.Packages.DeleteProjectPackage(repo.FullName(), o.packageID, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to delete package: %w", err)
	}

	o.io.LogInfof(color.Bold("%s Package %d deleted.\n"), color.RedCheck(), o.packageID)

	return nil
}
