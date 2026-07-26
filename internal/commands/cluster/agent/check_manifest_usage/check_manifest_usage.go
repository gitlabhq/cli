package check_manifest_usage

import (
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	group          string
	recursive      bool
	projectPerPage int
	projectPage    int
	agentPerPage   int
	agentPage      int

	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
}

type AgentConfig struct {
	GitOps struct {
		ManifestProjects []struct{} `yaml:"manifest_projects,omitempty"`
	} `yaml:"gitops,omitempty"`
}

func NewCmdCheckManifestUsage(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}
	checkManifestUsageCmd := &cobra.Command{
		Use:   "check_manifest_usage [flags]",
		Short: `Find agents using deprecated GitOps manifest settings. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Searches a group and its descendant projects for registered agents whose
			configuration files include the deprecated %[1]sgitops.manifest_projects%[1]s setting.

			The output is tab-separated and can be redirected to a TSV file.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Check a group for agents using deprecated GitOps manifest settings
			glab cluster agent check_manifest_usage --group my-group

			# Recursively check a group and all its subgroups
			glab cluster agent check_manifest_usage --group my-group --recursive`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}
	// Boolean to authorize experimental features
	checkManifestUsageCmd.Flags().StringVarP(&opts.group, "group", "g", "", "Group ID to check.")
	cobra.CheckErr(checkManifestUsageCmd.MarkFlagRequired("group"))
	checkManifestUsageCmd.Flags().IntVarP(&opts.projectPage, "page", "p", 1, "Page number for projects.")
	checkManifestUsageCmd.Flags().IntVarP(&opts.projectPerPage, "per-page", "P", 30, "Number of projects to list per page.")
	checkManifestUsageCmd.Flags().IntVarP(&opts.agentPage, "agent-page", "a", 1, "Page number for agents.")
	checkManifestUsageCmd.Flags().IntVarP(&opts.agentPerPage, "agent-per-page", "A", 30, "Number of agents to list per page.")
	checkManifestUsageCmd.Flags().BoolVarP(&opts.recursive, "recursive", "r", false, "Recursively check subgroups.")

	return checkManifestUsageCmd
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	// new line
	err = checkGroup(client, o.group, o)
	if err != nil {
		return err
	}

	if o.recursive {
		var groups []*gitlab.Group
		groups, _, err = listAllGroupsForGroup(client, o.group)
		if err != nil {
			return err
		}

		for _, group := range groups {
			err = checkGroup(client, group.FullPath, o)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func checkGroup(apiClient *gitlab.Client, group string, opts *options) error {
	var projects []*gitlab.Project
	var resp *gitlab.Response
	projects, resp, err := listAllProjectsForGroup(apiClient, group, *opts)
	if err != nil {
		return err
	}

	color := opts.io.Color()
	opts.io.LogInfo(color.ProgressIcon(), fmt.Sprintf("Checking %d of %d projects (Page %d of %d)\n", len(projects), resp.TotalItems, resp.CurrentPage, resp.TotalPages))
	for _, prj := range projects {
		err = checkManifestUsageInProject(apiClient, opts, prj)
		if err != nil {
			return err
		}
	}

	return nil
}

func listAllGroupsForGroup(apiClient *gitlab.Client, group string) ([]*gitlab.Group, *gitlab.Response, error) {
	l := &gitlab.ListDescendantGroupsOptions{}
	return apiClient.Groups.ListDescendantGroups(group, l)
}

func listAllProjectsForGroup(apiClient *gitlab.Client, group string, opts options) ([]*gitlab.Project, *gitlab.Response, error) {
	l := &gitlab.ListGroupProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: int64(opts.projectPerPage),
			Page:    int64(opts.projectPage),
		},
	}

	return apiClient.Groups.ListGroupProjects(group, l)
}

func checkManifestUsageInProject(apiClient *gitlab.Client, opts *options, project *gitlab.Project) error {
	color := opts.io.Color()
	opts.io.StartSpinner(fmt.Sprintf("Checking project %s for agents.\n", project.PathWithNamespace))
	defer opts.io.StopSpinner("")

	agents, _, err := apiClient.ClusterAgents.ListAgents(project.ID, &gitlab.ListAgentsOptions{
		ListOptions: gitlab.ListOptions{
			Page:    int64(opts.agentPage),
			PerPage: int64(opts.agentPerPage),
		},
	})
	if err != nil {
		return err
	}

	opts.io.LogInfo(color.ProgressIcon(), fmt.Sprintf("Found %d agents.\n", len(agents)))
	for _, agent := range agents {
		found, err := agentUsesManifestProjects(apiClient, opts, agent)
		if err != nil {
			opts.io.LogError(color.RedCheck(), "An error happened.", err)
			continue
		}
		if found {
			opts.io.LogInfo(fmt.Sprintf("%s\t%s\t%d", agent.ConfigProject.PathWithNamespace, agent.Name, 1))
		} else {
			opts.io.LogInfo(fmt.Sprintf("%s\t%s\t%d", agent.ConfigProject.PathWithNamespace, agent.Name, 0))
		}
	}

	return nil
}

func agentUsesManifestProjects(apiClient *gitlab.Client, opts *options, agent *gitlab.Agent) (bool, error) {
	color := opts.io.Color()
	opts.io.StartSpinner(fmt.Sprintf("Checking manifests of agent %s.\n", agent.Name))
	defer opts.io.StopSpinner("")

	// GetRawFile
	file, _, err := apiClient.RepositoryFiles.GetRawFile(agent.ConfigProject.ID, ".gitlab/agents/"+agent.Name+"/config.yaml", &gitlab.GetRawFileOptions{})
	if err != nil {
		if errors.Is(err, gitlab.ErrNotFound) {
			opts.io.LogInfo(color.WarnIcon(), fmt.Sprintf("Agent %s uses the default configuration.", agent.Name))
			return false, nil
		}
		return false, fmt.Errorf("failed to read configuration for agent %q: %w", agent.Name, err)
	}

	// Check that gitops.manifest_projects json path does not exist in configFile
	var configData AgentConfig
	err = yaml.Unmarshal(file, &configData)
	if err != nil {
		opts.io.LogError("Unmarshal error", fmt.Sprintf("%s\n", string(file)))
		return false, err
	}

	if len(configData.GitOps.ManifestProjects) == 0 {
		opts.io.LogInfo(color.GreenCheck(), fmt.Sprintf("Agent %s does not have manifest projects configured.", agent.Name))
		return false, nil
	} else {
		opts.io.LogInfo(color.FailedIcon(), fmt.Sprintf("Agent %s has %d manifest projects configured.", agent.Name, len(configData.GitOps.ManifestProjects)))
		return true, nil
	}
}
