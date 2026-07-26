package check_manifest_usage

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAgentUsesManifestProjectsHandlesReadErrors(t *testing.T) {
	tests := []struct {
		name    string
		readErr error
		wantErr bool
	}{
		{
			name:    "missing configuration uses defaults",
			readErr: gitlab.ErrNotFound,
		},
		{
			name:    "other read errors are returned",
			readErr: errors.New("repository file request failed"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			ios, _, _, _ := cmdtest.TestIOStreams()
			agent := &gitlab.Agent{
				Name:          "my-agent",
				ConfigProject: gitlab.ConfigProject{ID: 123},
			}

			testClient.MockRepositoryFiles.EXPECT().
				GetRawFile(int64(123), ".gitlab/agents/my-agent/config.yaml", gomock.Any()).
				Return(nil, nil, tt.readErr)

			found, err := agentUsesManifestProjects(testClient.Client, &options{io: ios}, agent)

			assert.False(t, found)
			if tt.wantErr {
				require.ErrorIs(t, err, tt.readErr)
				assert.Contains(t, err.Error(), `failed to read configuration for agent "my-agent"`)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
