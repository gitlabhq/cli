//go:build !integration

package auth

import (
	"errors"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_GetAuthenticatedClient(t *testing.T) {
	tests := []struct {
		name       string
		HttpClient func() (*gitlab.Client, error)
		Config     config.Config
		wantErr    bool
	}{
		{
			name:   "everything ok!",
			Config: config.NewBlankConfig(),
			HttpClient: func() (*gitlab.Client, error) {
				tc := gitlab_testing.NewTestClient(t)
				return tc.Client, nil
			},
		},
		{
			name: "no hosts",
			Config: config.NewFromString(heredoc.Doc(`
				hosts:
			`)),
			HttpClient: func() (*gitlab.Client, error) {
				tc := gitlab_testing.NewTestClient(t)
				return tc.Client, nil
			},
			wantErr: true,
		},
		{
			name:   "bad httpclient",
			Config: config.NewBlankConfig(),
			HttpClient: func() (*gitlab.Client, error) {
				return nil, errors.New("oopsies")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := cmdtest.TestIOStreams()

			_, err := GetAuthenticatedClient(tt.Config, tt.HttpClient, ios)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
