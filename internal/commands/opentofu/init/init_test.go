//go:build !integration

package init

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestInit_CommandConstruction(t *testing.T) {
	t.Parallel()

	// GIVEN
	ctrl := gomock.NewController(t)
	tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://gitlab.example.com"))
	execMock := cmdtest.NewMockExecutor(ctrl)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "testtoken", "gitlab.example.com", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithExecutor(execMock),
	)

	execMock.EXPECT().Exec(gomock.Any(), "my-tofu", []string{
		"-chdir=infra",
		"init",
		"-backend-config=address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production",
		"-backend-config=lock_address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production/lock",
		"-backend-config=unlock_address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production/lock",
		"-backend-config=lock_method=POST",
		"-backend-config=unlock_method=DELETE",
		"-backend-config=retry_wait_min=5",
		"-backend-config=headers={\"Authorization\" = \"Bearer testtoken\"}",
	}, nil)

	// WHEN
	_, err := exec("production -d infra -b my-tofu")
	require.NoError(t, err)
}

func TestInit_CommandConstruction_InitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stateName              string
		expectedStateTFURLPart string
	}{
		{
			stateName:              "production",
			expectedStateTFURLPart: "terraform/state/production",
		},
		{
			stateName:              "production/iac",
			expectedStateTFURLPart: "terraform/state/production%2Fiac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.stateName, func(t *testing.T) {
			t.Parallel()

			// GIVEN
			ctrl := gomock.NewController(t)
			tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://gitlab.example.com"))
			execMock := cmdtest.NewMockExecutor(ctrl)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "testtoken", "gitlab.example.com", api.WithGitLabClient(tc.Client))),
				cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
				cmdtest.WithExecutor(execMock),
			)

			execMock.EXPECT().Exec(gomock.Any(), "my-tofu", []string{
				"-chdir=infra",
				"init",
				fmt.Sprintf("-backend-config=address=https://gitlab.example.com/api/v4/projects/OWNER%%2FREPO/%s", tt.expectedStateTFURLPart),
				fmt.Sprintf("-backend-config=lock_address=https://gitlab.example.com/api/v4/projects/OWNER%%2FREPO/%s/lock", tt.expectedStateTFURLPart),
				fmt.Sprintf("-backend-config=unlock_address=https://gitlab.example.com/api/v4/projects/OWNER%%2FREPO/%s/lock", tt.expectedStateTFURLPart),
				"-backend-config=lock_method=POST",
				"-backend-config=unlock_method=DELETE",
				"-backend-config=retry_wait_min=5",
				"-backend-config=headers={\"Authorization\" = \"Bearer testtoken\"}",
				"-reconfigure",
			}, nil)

			// WHEN
			_, err := exec(fmt.Sprintf("%s -d infra -b my-tofu -- -reconfigure", tt.stateName))
			require.NoError(t, err)
		})
	}
}

// An OAuth2 host whose refresh token is missing resolves to an access-token-only
// auth source, which init used to reject outright.
func TestInit_CommandConstruction_OAuth2AccessTokenOnly(t *testing.T) {
	for _, k := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "GLAB_IS_OAUTH2"} {
		t.Setenv(k, "")
	}

	ctrl := gomock.NewController(t)
	execMock := cmdtest.NewMockExecutor(ctrl)
	cfg := config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.example.com:
		    is_oauth2: "true"
		    token: an-access-token
		    api_protocol: https
		    api_host: gitlab.example.com
	`))

	apiClient, err := api.NewClientFromConfig("gitlab.example.com", cfg, false, "glab test client")
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithConfig(cfg),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithExecutor(execMock),
	)

	execMock.EXPECT().Exec(gomock.Any(), "my-tofu", gomock.Cond(func(args []string) bool {
		return slices.Contains(args, `-backend-config=headers={"Authorization" = "Bearer an-access-token"}`)
	}), nil)

	_, err = exec("production -d infra -b my-tofu")
	require.NoError(t, err)
}

// unclassifiableAuthSource is not something NewClientFromConfig can produce, so
// this only guards the contract for whoever adds the next auth source.
type unclassifiableAuthSource struct{}

func (unclassifiableAuthSource) Init(context.Context, *gitlab.Client) error { return nil }

func (unclassifiableAuthSource) Header(context.Context) (string, string, error) {
	return "X-Custom", "a-value", nil
}

func TestInit_CommandConstruction_credentialErrors(t *testing.T) {
	tests := []struct {
		name        string
		authSource  gitlab.AuthSource
		expectedErr string
		refutedErr  string
	}{
		{
			name:        "an unclassifiable auth source names the requirement, not the Go type",
			authSource:  unclassifiableAuthSource{},
			expectedErr: "init command does not support this authentication method",
			refutedErr:  "unclassifiableAuthSource",
		},
		{
			name:        "an unauthenticated host is reported once, not wrapped",
			authSource:  gitlab.Unauthenticated{},
			expectedErr: "glab is not authenticated",
			refutedErr:  "unable to retrieve an access token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, tt.authSource, "gitlab.example.com")),
				cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
			)

			_, err := exec("production")

			require.Error(t, err)
			require.ErrorContains(t, err, tt.expectedErr)
			assert.NotContains(t, err.Error(), tt.refutedErr)
		})
	}
}
