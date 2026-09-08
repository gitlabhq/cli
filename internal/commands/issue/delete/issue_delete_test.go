//go:build !integration

package delete

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "issue_delete_test")
}

func TestNewCmdDelete(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	oldDeleteMR := api.DeleteMR

	deleteIssue = func(_ *gitlab.Client, projectID any, issueID int64) error {
		if projectID == "" || projectID == "NAMESPACE/WRONG_REPO" || projectID == "expected_err" || issueID == 0 {
			return fmt.Errorf("error expected")
		}
		return nil
	}
	api.GetIssue = func(client *gitlab.Client, projectID any, issueID int64) (*gitlab.Issue, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" {
			return nil, fmt.Errorf("error expected")
		}
		return &gitlab.Issue{
			IID: issueID,
		}, nil
	}

	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		errMsg     string
		assertFunc func(*testing.T, string, string)
	}{
		{
			name:    "delete",
			args:    []string{"0", "-R", "NAMESPACE/WRONG_REPO"},
			wantErr: true,
		},
		{
			name:    "id exists",
			args:    []string{"1"},
			wantErr: false,
			assertFunc: func(t *testing.T, out string, err string) {
				t.Helper()

				assert.Contains(t, err, "✓ Issue deleted.\n")
			},
		},
		{
			name:    "delete on different repo",
			args:    []string{"12", "-R", "profclems/glab"},
			wantErr: false,
			assertFunc: func(t *testing.T, out string, stderr string) {
				t.Helper()

				assert.Contains(t, stderr, "✓ Issue deleted.\n")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(
				t,
				func(f cmdutils.Factory) *cobra.Command {
					cmd := NewCmdDelete(f)
					cmd.Flags().StringP("repo", "R", "", "")
					return cmd
				},
				false,
			)

			cli := strings.Join(tt.args, " ")
			t.Log(cli)
			cmdOut, err := exec(cli)
			if !tt.wantErr {
				require.NoError(t, err)
				tt.assertFunc(t, cmdOut.OutBuf.String(), cmdOut.ErrBuf.String())
			} else {
				require.Error(t, err)
			}
		})
	}

	api.DeleteMR = oldDeleteMR
}
