//go:build !integration

package delete

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/acarl005/stripansi"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// TODO: test by mocking the appropriate api function
func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "mr_delete_test")
}

func Test_deleteMergeRequest(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdtest.NewTestFactory(
		io,
		cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.com:
			    username: monalisa
			    token: OTOKEN
	    `))),
		cmdtest.WithBranch("master"),
	)
	oldDeleteMR := api.DeleteMR

	api.DeleteMR = func(client *gitlab.Client, projectID any, mrID int64) error {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" || mrID == 0 {
			return fmt.Errorf("error expected")
		}
		return nil
	}

	api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" {
			return nil, fmt.Errorf("error expected")
		}
		repo, err := f.BaseRepo()
		if err != nil {
			return nil, err
		}
		return &gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          mrID,
				IID:         mrID,
				Title:       "mrTitle",
				Labels:      gitlab.Labels{"test", "bug"},
				State:       "opened",
				Description: "mrBody",
				Author: &gitlab.BasicUser{
					ID:       mrID,
					Name:     "John Dev Wick",
					Username: "jdwick",
				},
				WebURL: fmt.Sprintf("https://%s/%s/-/merge_requests/%d", repo.RepoHost(), repo.FullName(), mrID),
			},
		}, nil
	}

	api.ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
		return []*gitlab.BasicMergeRequest{}, nil
	}

	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		errMsg     string
		assertFunc func(*testing.T, string, string, error)
	}{
		{
			name:    "delete",
			args:    []string{"0"},
			wantErr: true,

			assertFunc: func(t *testing.T, out, outErr string, err error) {
				t.Helper()

				assert.Equal(t, "invalid merge request ID provided", err.Error())
			},
		},
		{
			name:    "id exists",
			args:    []string{"1"},
			wantErr: false,
			assertFunc: func(t *testing.T, out, outErr string, err error) {
				t.Helper()

				assert.Contains(t, out, "- Deleting merge request !1.\n")
				assert.Contains(t, out, "✓ Merge request !1 deleted.\n")
			},
		},
		{
			name:    "delete on different repo",
			args:    []string{"1", "-R", "profclems/glab"},
			wantErr: false,
			assertFunc: func(t *testing.T, out, outErr string, err error) {
				t.Helper()

				assert.Contains(t, out, "- Deleting merge request !1.\n")
				assert.Contains(t, out, "✓ Merge request !1 deleted.\n")
			},
		},
		{
			name:    "delete no args",
			wantErr: true,
			assertFunc: func(t *testing.T, out, outErr string, err error) {
				t.Helper()

				assert.Equal(t, `no open merge request available for "master"`, err.Error())
			},
		},
	}

	cmd := NewCmdDelete(f)

	cmd.Flags().StringP("repo", "R", "", "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := strings.Join(tt.args, " ")
			t.Log(cli)
			argv, err := shlex.Split(cli)
			if err != nil {
				t.Fatal(err)
			}
			cmd.SetArgs(argv)
			_, err = cmd.ExecuteC()
			if !tt.wantErr {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			out := stripansi.Strip(stdout.String())
			outErr := stripansi.Strip(stderr.String())
			t.Log(outErr)

			tt.assertFunc(t, out, outErr, err)
			assert.Contains(t, outErr, tt.errMsg)
		})
	}

	api.DeleteMR = oldDeleteMR
}
