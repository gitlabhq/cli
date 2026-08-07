package list

import (
	"context"
	"fmt"
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
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)

	listOptions *gitlab.ListProjectPackagesOptions
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: `List packages in a project's package registry.`,
		Long: heredoc.Docf(`
		Packages of all types (generic, npm, maven, etc.) are returned. Use
		%[1]s--package-type%[1]s to filter by type and %[1]s--name%[1]s to filter by name. Use
		%[1]s--page%[1]s and %[1]s--per-page%[1]s to paginate the result.

		By default, packages are listed for the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
			# List all packages in the current project
			glab packages list

			# Use the 'ls' alias
			glab packages ls

			# Filter by package name
			glab packages list --name my-package

			# List a specific page with a custom page size
			glab packages list --page 2 --per-page 10

			# List packages from another project
			glab packages list -R owner/repo
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.IntP("page", "p", 1, "Page number.")
	fl.IntP("per-page", "P", int(api.DefaultListLimit), "Number of items to list per page.")
	fl.StringP("name", "n", "", "Filter packages by name (substring match).")

	pkgTypes := []string{
		"composer",
		"conan",
		"debian",
		"generic",
		"golang",
		"helm",
		"maven",
		"npm",
		"nuget",
		"pypi",
		"terraform_module",
	}
	fl.String("package-type", "", fmt.Sprintf("Filter packages by type. One of: %s.", strings.Join(pkgTypes, ", ")))

	cmdutils.AddJQFlag(cmd, f.IO())
	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	o.listOptions = &gitlab.ListProjectPackagesOptions{
		ListOptions: gitlab.ListOptions{
			Page:    1,
			PerPage: api.DefaultListLimit,
		},
	}

	fl := cmd.Flags()

	if fl.Changed("page") {
		page, err := fl.GetInt("page")
		if err != nil {
			return err
		}
		o.listOptions.Page = int64(page)
	}

	if fl.Changed("per-page") {
		perPage, err := fl.GetInt("per-page")
		if err != nil {
			return err
		}
		o.listOptions.PerPage = int64(perPage)
	}

	if fl.Changed("name") {
		name, err := fl.GetString("name")
		if err != nil {
			return err
		}
		o.listOptions.PackageName = &name
	}

	if fl.Changed("package-type") {
		pkgType, err := fl.GetString("package-type")
		if err != nil {
			return err
		}
		o.listOptions.PackageType = &pkgType
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

	packages, _, err := client.Packages.ListProjectPackages(repo.FullName(), o.listOptions, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	return o.io.PrintJSON(packages)
}
