//go:build !integration

package disable

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

func TestDisable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		profile    string
		mockAPI    bool
		detachErr  error
		wantErr    bool
		wantErrMsg string
		wantOut    string
	}{
		{
			name:    "valid profile",
			profile: "dependency_scanning",
			mockAPI: true,
			wantOut: "✓ Disabled the \"dependency_scanning\" security scan profile for OWNER/REPO.\n",
		},
		{
			name:       "missing profile",
			profile:    "",
			wantErr:    true,
			wantErrMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:       "feature disabled or unauthorized",
			profile:    "dependency_scanning",
			mockAPI:    true,
			detachErr:  errors.New("The resource that you are attempting to access does not exist"),
			wantErr:    true,
			wantErrMsg: "failed to disable the profile",
		},
		{
			name:       "unknown profile",
			profile:    "unknown_profile",
			mockAPI:    true,
			detachErr:  errors.New("The resource that you are attempting to access does not exist or you don't have permission to perform this action"),
			wantErr:    true,
			wantErrMsg: "failed to disable the profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			if tt.mockAPI {
				tc.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{ID: 123}, nil, nil).
					Times(1)
				tc.MockSecurityScanProfiles.EXPECT().
					DetachSecurityScanProfile(&gitlab.DetachSecurityScanProfileOptions{
						SecurityScanProfileID: gitlab.SecurityScanProfileGID(tt.profile),
						ProjectIDs:            []int64{123},
					}, gomock.Any()).
					Return(&gitlab.Response{}, tt.detachErr).
					Times(1)
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
			out, err := exec(tt.profile)

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
