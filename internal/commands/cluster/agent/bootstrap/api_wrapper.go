package bootstrap

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	glab_api "gitlab.com/gitlab-org/cli/internal/api"
)

const (
	// agentTokenLimit specifies the maximal amount of agent tokens that can be active per agent at any given time.
	agentTokenLimit = 2
)

var agentNotFoundErr = errors.New("agent not found")

var _ API = (*apiWrapper)(nil)

type commitAuthor struct {
	name, email string
}

func NewAPI(client *gitlab.Client, projectID any, commitAuthor *commitAuthor) API {
	return &apiWrapper{client: client, projectID: projectID, commitAuthor: commitAuthor}
}

type apiWrapper struct {
	client       *gitlab.Client
	projectID    any
	commitAuthor *commitAuthor
}

func (a *apiWrapper) GetDefaultBranch() (string, error) {
	project, err := glab_api.GetProject(a.client, a.projectID)
	if err != nil {
		return "", err
	}
	return project.DefaultBranch, nil
}

func (a *apiWrapper) GetAgentByName(name string) (*gitlab.Agent, error) {
	opts := &gitlab.ListAgentsOptions{
		ListOptions: gitlab.ListOptions{
			Page:    1,
			PerPage: 100,
		},
	}

	for agent, err := range gitlab.Scan2(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Agent, *gitlab.Response, error) {
		return a.client.ClusterAgents.ListAgents(a.projectID, opts, p)
	}) {
		if err != nil {
			return nil, err
		}

		if agent.Name == name {
			// found
			return agent, nil
		}
	}

	return nil, agentNotFoundErr
}

func (a *apiWrapper) RegisterAgent(name string) (*gitlab.Agent, error) {
	agent, _, err := a.client.ClusterAgents.RegisterAgent(a.projectID, &gitlab.RegisterAgentOptions{Name: new(name)})
	return agent, err
}

type agentConfig struct {
	UserAccess *agentConfigUserAccess `yaml:"user_access"`
}

type agentConfigUserAccess struct {
	AccessAs *agentConfigAccessAs  `yaml:"access_as"`
	Projects []*agentConfigProject `yaml:"projects"`
}

type agentConfigAccessAs struct {
	Agent struct{}
}

type agentConfigProject struct {
	ID string `yaml:"id"`
}

func (a *apiWrapper) ConfigureAgent(agent *gitlab.Agent, branch string) error {
	configPath := fmt.Sprintf(".gitlab/agents/%s/config.yaml", agent.Name)
	file, _, err := a.client.RepositoryFiles.GetFile(a.projectID, configPath, &gitlab.GetFileOptions{Ref: new(branch)})
	if err != nil && !glab_api.Is404(err) {
		return err
	}

	cfg := agentConfig{}
	if glab_api.Is404(err) {
		cfg.UserAccess = &agentConfigUserAccess{
			AccessAs: &agentConfigAccessAs{Agent: struct{}{}},
			Projects: []*agentConfigProject{
				{
					ID: agent.ConfigProject.PathWithNamespace,
				},
			},
		}

		configuredContent, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}

		opts := &gitlab.CreateFileOptions{
			Branch:        new(branch),
			Content:       new(string(configuredContent)),
			CommitMessage: new(fmt.Sprintf("Add %s via glab file sync", configPath)),
		}

		if a.commitAuthor != nil {
			opts.AuthorName = new(a.commitAuthor.name)
			opts.AuthorEmail = new(a.commitAuthor.email)
		}

		_, _, err = a.client.RepositoryFiles.CreateFile(a.projectID, configPath, opts)
		return err
	} else {
		content, err := base64.StdEncoding.DecodeString(file.Content)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(content, &cfg)
		if err != nil {
			return err
		}

		if cfg.UserAccess == nil {
			cfg.UserAccess = &agentConfigUserAccess{
				AccessAs: &agentConfigAccessAs{Agent: struct{}{}},
			}
		}

		if !slices.ContainsFunc(cfg.UserAccess.Projects, func(p *agentConfigProject) bool { return p.ID == agent.ConfigProject.PathWithNamespace }) {
			cfg.UserAccess.Projects = append(cfg.UserAccess.Projects, &agentConfigProject{ID: agent.ConfigProject.PathWithNamespace})
		}

		configuredContent, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}

		opts := &gitlab.UpdateFileOptions{
			Branch:        new(branch),
			Content:       new(string(configuredContent)),
			CommitMessage: new(fmt.Sprintf("Update %s via glab file sync", configPath)),
		}

		if a.commitAuthor != nil {
			opts.AuthorName = new(a.commitAuthor.name)
			opts.AuthorEmail = new(a.commitAuthor.email)
		}

		_, _, err = a.client.RepositoryFiles.UpdateFile(a.projectID, configPath, opts)
		return err
	}
}

func (a *apiWrapper) ConfigureEnvironment(agentID int64, name string, kubernetesNamespace string, fluxResourcePath string) error {
	env, err := a.getEnvironmentByName(name)
	if err != nil {
		return err
	}

	if env == nil {
		_, _, err := a.client.Environments.CreateEnvironment(a.projectID, &gitlab.CreateEnvironmentOptions{
			Name:                new(name),
			ClusterAgentID:      new(agentID),
			KubernetesNamespace: new(kubernetesNamespace),
			FluxResourcePath:    new(fluxResourcePath),
		})
		return err
	} else {
		_, _, err := a.client.Environments.EditEnvironment(a.projectID, env.ID, &gitlab.EditEnvironmentOptions{
			Name:                new(name),
			ClusterAgentID:      new(agentID),
			KubernetesNamespace: new(kubernetesNamespace),
			FluxResourcePath:    new(fluxResourcePath),
		})
		return err
	}
}

func (a *apiWrapper) CreateAgentToken(agentID int64) (*gitlab.AgentToken, error) {
	tokens, _, err := a.client.ClusterAgents.ListAgentTokens(a.projectID, agentID, &gitlab.ListAgentTokensOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: agentTokenLimit,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(tokens) == agentTokenLimit {
		slices.SortFunc(tokens, agentTokenSortFunc)
		longestUnusedToken := tokens[0]

		_, err := a.client.ClusterAgents.RevokeAgentToken(a.projectID, agentID, longestUnusedToken.ID)
		if err != nil {
			return nil, err
		}
	}

	// create new token
	token, _, err := a.client.ClusterAgents.CreateAgentToken(a.projectID, agentID, &gitlab.CreateAgentTokenOptions{
		Name:        new(fmt.Sprintf("glab-bootstrap-%d", time.Now().UTC().Unix())),
		Description: new("Created by the `glab cluster agent bootstrap command"),
	})
	return token, err
}

func (a *apiWrapper) SyncFile(f file, branch string) error {
	_, _, err := a.client.RepositoryFiles.GetFileMetaData(a.projectID, f.path, &gitlab.GetFileMetaDataOptions{
		Ref: new(branch),
	})
	if err != nil {
		if !gitlab.HasStatusCode(err, http.StatusNotFound) {
			return err
		}

		opts := &gitlab.CreateFileOptions{
			Branch:        new(branch),
			Content:       new(string(f.content)),
			CommitMessage: new(fmt.Sprintf("Add %s via glab file sync", f.path)),
		}

		if a.commitAuthor != nil {
			opts.AuthorName = new(a.commitAuthor.name)
			opts.AuthorEmail = new(a.commitAuthor.email)
		}

		// file does not exist yet, lets create it
		_, _, err := a.client.RepositoryFiles.CreateFile(a.projectID, f.path, opts)
		return err
	}

	opts := &gitlab.UpdateFileOptions{
		Branch:        new(branch),
		Content:       new(string(f.content)),
		CommitMessage: new(fmt.Sprintf("Update %s via glab file sync", f.path)),
	}

	if a.commitAuthor != nil {
		opts.AuthorName = new(a.commitAuthor.name)
		opts.AuthorEmail = new(a.commitAuthor.email)
	}

	// file already exists, lets update it
	_, _, err = a.client.RepositoryFiles.UpdateFile(a.projectID, f.path, opts)
	return err
}

func (a *apiWrapper) GetKASAddress() (string, error) {
	metadata, _, err := a.client.Metadata.GetMetadata()
	if err != nil {
		return "", err
	}

	if !metadata.KAS.Enabled {
		return "", errors.New("KAS is not configured in this GitLab instance; contact your administrator")
	}

	return metadata.KAS.ExternalURL, nil
}

func (a *apiWrapper) getEnvironmentByName(name string) (*gitlab.Environment, error) {
	opts := &gitlab.ListEnvironmentsOptions{
		Name: new(name),
	}

	envs, _, err := a.client.Environments.ListEnvironments(a.projectID, opts)
	if err != nil {
		if glab_api.Is404(err) {
			return nil, nil
		}
		return nil, err
	}

	if len(envs) == 0 {
		return nil, nil
	}

	return envs[0], nil
}

func agentTokenSortFunc(a, b *gitlab.AgentToken) int {
	if a.LastUsedAt == nil {
		return 1
	}
	if b.LastUsedAt == nil {
		return -1
	}
	return a.LastUsedAt.Compare(*b.LastUsedAt)
}
