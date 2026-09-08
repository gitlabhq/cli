//go:build !integration

package status

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cli        string
		mockAPI    bool
		statuses   []gitlab.ScanProfileStatus
		statusErr  error
		wantErr    bool
		wantErrMsg string
		wantOut    string
	}{
		{
			name:    "attached and active",
			cli:     "dependency_scanning",
			mockAPI: true,
			statuses: []gitlab.ScanProfileStatus{
				{
					Status: "ACTIVE",
					ScanProfile: gitlab.ScanProfile{
						ScanType: "DEPENDENCY_SCANNING",
					},
				},
			},
			wantOut: "dependency_scanning profile for OWNER/REPO: ACTIVE\n",
		},
		{
			name:     "not configured",
			cli:      "dependency_scanning",
			mockAPI:  true,
			statuses: []gitlab.ScanProfileStatus{},
			wantOut:  "dependency_scanning profile for OWNER/REPO: NOT_CONFIGURED\n",
		},
		{
			name:       "missing profile",
			cli:        "",
			wantErr:    true,
			wantErrMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:     "custom profile not configured",
			cli:      "my_custom_profile",
			mockAPI:  true,
			statuses: []gitlab.ScanProfileStatus{},
			wantOut:  "my_custom_profile profile for OWNER/REPO: NOT_CONFIGURED\n",
		},
		{
			name:       "feature disabled or unauthorized",
			cli:        "dependency_scanning",
			mockAPI:    true,
			statusErr:  errors.New("The resource that you are attempting to access does not exist"),
			wantErr:    true,
			wantErrMsg: "failed to read the status of the profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			if tt.mockAPI {
				tc.MockSecurityScanProfiles.EXPECT().
					ListProjectScanProfileStatuses("OWNER/REPO", gomock.Any()).
					Return(tt.statuses, &gitlab.Response{}, tt.statusErr).
					Times(1)
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
			out, err := exec(tt.cli)

			if tt.wantErr {
				require.Error(t, err)
				if !tt.mockAPI {
					// Cobra's own arg validation error, not wrapped in an ExitError.
					assert.Contains(t, err.Error(), tt.wantErrMsg)
					return
				}
				var exitErr *cmdutils.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Contains(t, exitErr.Details, tt.wantErrMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOut, out.OutBuf.String())
		})
	}
}
