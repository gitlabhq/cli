//go:build !integration

package unsubscribe

import (
	"fmt"
	"testing"
	"time"

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

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "")
}

func TestNewCmdUnsubscribe(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	oldUnsubscribeMR := unsubscribeFromMR
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	unsubscribeFromMR = func(client *gitlab.Client, projectID any, mrID int64, opts gitlab.RequestOptionFunc) (*gitlab.MergeRequest, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" || mrID == 0 {
			return nil, fmt.Errorf("error expected")
		}
		repo, err := f.BaseRepo()
		if err != nil {
			return nil, err
		}
		return &gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          1,
				IID:         1,
				Title:       "mrtitile",
				Labels:      gitlab.Labels{"bug", "test"},
				State:       "opened",
				Description: "mrbody",
				Author: &gitlab.BasicUser{
					ID:       1,
					Name:     "John Dev Wick",
					Username: "jdwick",
				},
				WebURL:    "https://" + repo.RepoHost() + "/" + repo.FullName() + "/-/merge_requests/1",
				CreatedAt: &timer,
			},
			Subscribed: false,
		}, nil
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
			Subscribed: true,
		}, nil
	}

	testCases := []struct {
		Name        string
		Issue       string
		ExpectedMsg []string
		wantErr     bool
	}{
		{
			Name:        "Issue Exists",
			Issue:       "1",
			ExpectedMsg: []string{"- Unsubscribing from merge request !1.", "✓ You have successfully unsubscribed from merge request !1."},
		},
		{
			Name:        "Issue on another repo",
			Issue:       "1 -R profclems/glab",
			ExpectedMsg: []string{"- Unsubscribing from merge request !1.", "✓ You have successfully unsubscribed from merge request !1."},
		},
		{
			Name:        "Issue Does Not Exist",
			Issue:       "0",
			ExpectedMsg: []string{"- Unsubscribing from merge request !0.", "error expected"},
			wantErr:     true,
		},
	}

	cmd := NewCmdUnsubscribe(f)
	cmd.Flags().StringP("repo", "R", "", "")

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			argv, err := shlex.Split(tc.Issue)
			if err != nil {
				t.Fatal(err)
			}
			cmd.SetArgs(argv)
			_, err = cmd.ExecuteC()
			if err != nil {
				if tc.wantErr {
					require.Error(t, err)
					return
				} else {
					t.Fatal(err)
				}
			}

			out := stripansi.Strip(stdout.String())

			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out, msg)
				assert.Empty(t, stderr.String())
			}
		})
	}

	unsubscribeFromMR = oldUnsubscribeMR
}
