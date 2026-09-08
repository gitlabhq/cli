package catalog

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	publishToCatalogApiPath = "projects/%s/catalog/publish"
	templatesDirName        = "templates"
	templateFileExt         = ".yml"
	templateFileName        = "template.yml"
)

type publishToCatalogRequest struct {
	Version  string         `json:"version"`
	Metadata map[string]any `json:"metadata"`
}

type publishToCatalogResponse struct {
	CatalogUrl string `json:"catalog_url"`
}

type options struct {
	tagName string

	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	io           *iostreams.IOStreams
}

func NewCmdPublishCatalog(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	publishCatalogCmd := &cobra.Command{
		Use:   "catalog <tag-name>",
		Short: `[EXPERIMENTAL] Publishes CI/CD components to the catalog.`,
		Long: heredoc.Docf(`[EXPERIMENTAL] Publishes CI/CD components in the project to the CI/CD catalog using the provided tag name.

    Requires the feature flag %[1]sci_release_cli_catalog_publish_option%[1]s to be enabled
    for this project in your GitLab instance.

    Requires the same user as the release author.

    - It retrieves components from the current repository by searching for
      %[1]syml%[1]s files within the "templates" directory and its subdirectories.
    - It fails if the feature flag %[1]sci_release_cli_catalog_publish_option%[1]s
      is not enabled for this project in your GitLab instance.

    Components can be defined:

    - In single files ending in %[1]s.yml%[1]s for each component, like %[1]stemplates/secret-detection.yml%[1]s.
    - In subdirectories containing %[1]stemplate.yml%[1]s files as entry points,
      for components that bundle together multiple related files. For example,
      %[1]stemplates/secret-detection/template.yml%[1]s.
    `, "`"),
		Example: heredoc.Doc(`
			glab repo publish catalog v1.2.3`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run()
		},
	}

	return publishCatalogCmd
}

func (o *options) complete(args []string) {
	o.tagName = args[0]
}

func (o *options) run() error {
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	_, _, err = client.Tags.GetTag(repo.FullName(), o.tagName)
	if err != nil {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid tag %s", o.tagName)}
	}

	return Publish(o.io, client, repo.FullName(), o.tagName)
}

func Publish(io *iostreams.IOStreams, client *gitlab.Client, repoFullName string, tagName string) error {
	color := io.Color()

	io.LogInfof("%s Publishing release %s=%s to the GitLab CI/CD catalog for %s=%s...\n",
		color.ProgressIcon(),
		color.Blue("tag"), tagName,
		color.Blue("repo"), repoFullName)

	body, err := publishToCatalogRequestBody(tagName)
	if err != nil {
		return cmdutils.WrapError(err, "failed to create a request body.")
	}

	path := fmt.Sprintf(publishToCatalogApiPath, url.PathEscape(repoFullName))
	request, err := client.NewRequest(http.MethodPost, path, body, nil)
	if err != nil {
		return cmdutils.WrapError(err, "failed to create a request.")
	}

	var response publishToCatalogResponse
	_, err = client.Do(request, &response)
	if err != nil {
		return err
	}

	io.LogInfof("%s Release published: %s=%s\n", color.GreenCheck(),
		color.Blue("url"), response.CatalogUrl)

	return nil
}

func publishToCatalogRequestBody(version string) (*publishToCatalogRequest, error) {
	baseDir, err := os.Getwd()
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to get working directory")
	}

	components, err := fetchTemplates(baseDir)
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to fetch components")
	}

	metadata := make(map[string]any)
	componentsData := make([]map[string]any, 0, len(components))

	for name, path := range components {
		spec, err := extractSpec(path)
		if err != nil {
			return nil, cmdutils.WrapError(err, "failed to extract spec")
		}

		componentsData = append(componentsData, map[string]any{
			"name":           name,
			"spec":           spec,
			"component_type": "template",
		})
	}

	sort.Slice(componentsData, func(i, j int) bool {
		// `name` is always set to a string literal a few lines above.
		ni, _ := componentsData[i]["name"].(string)
		nj, _ := componentsData[j]["name"].(string)
		return ni < nj
	})

	metadata["components"] = componentsData

	return &publishToCatalogRequest{
		Version:  version,
		Metadata: metadata,
	}, nil
}

// fetchTemplates returns a map of component names to their paths.
// The component name is either the name of the file without the extension in the "templates" directory of the project
// or the name of the directory containing a "template.yml" file in the "templates" directory.
// More information: https://docs.gitlab.com/ci/components/#directory-structure
func fetchTemplates(baseDir string) (map[string]string, error) {
	templates := make(map[string]string)

	paths, err := fetchTemplatePaths(baseDir)
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to fetch template paths")
	}

	for _, path := range paths {
		componentName, err := extractComponentName(baseDir, path)
		if err != nil {
			return nil, cmdutils.WrapError(err, "failed to extract component name")
		}

		if componentName != "" {
			templates[componentName] = path
		}
	}

	return templates, nil
}

// fetchTemplatePaths returns a list of the possible component paths to the YAML files in the "templates" directory.
func fetchTemplatePaths(baseDir string) ([]string, error) {
	templatesDir := filepath.Join(baseDir, templatesDirName)

	var yamlFiles []string

	err := filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return cmdutils.WrapError(err, "failed to walk directory")
		}

		if filepath.Ext(d.Name()) == templateFileExt {
			yamlFiles = append(yamlFiles, path)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return yamlFiles, nil
}

// extractComponentName returns the valid component name from the path if it is a valid component path.
// valid component paths:
// 1. All YAML files in the "templates" directory.
// 2. All "template.yml" files in the subdirectories of the "templates" directory.
func extractComponentName(baseDir string, path string) (string, error) {
	relativePath, err := filepath.Rel(baseDir, path)
	if err != nil {
		return "", err
	}

	dirname := filepath.Dir(relativePath)
	fileExt := filepath.Ext(relativePath)
	filename := filepath.Base(relativePath)
	filenameWithoutExt := filename[:len(filename)-len(fileExt)]

	// All YAML files in the "templates" directory.
	if dirname == templatesDirName {
		return filenameWithoutExt, nil
	}

	// All "template.yml" files in the subdirectories of the "templates" directory.
	if filename == templateFileName {
		return filepath.Base(dirname), nil
	} else {
		return "", nil
	}
}

type specDef struct {
	Spec map[string]any `yaml:"spec"`
}

// extractSpec returns the spec from the component file.
func extractSpec(componentPath string) (map[string]any, error) {
	content, err := os.ReadFile(componentPath)
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to read file")
	}

	var spec specDef

	err = yaml.Unmarshal(content, &spec)
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to unmarshal YAML")
	}

	return spec.Spec, nil
}
