//go:build integration

package update

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestNewCmdUpdate_Integration(t *testing.T) {
	glTestHost := test.GetHostOrSkip(t)

	oldUpdateIssue := api.UpdateIssue
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	testIssue := &gitlab.Issue{
		ID:               1,
		IID:              1,
		State:            "closed",
		Labels:           gitlab.Labels{"bug, test, removeable-label"},
		Description:      "Dummy description for issue 1",
		DiscussionLocked: false,
		Author: &gitlab.IssueAuthor{
			ID:       1,
			Name:     "John Dev Wick",
			Username: "jdwick",
		},
		CreatedAt: &timer,
	}
	api.UpdateIssue = func(client *gitlab.Client, projectID any, issueID int64, opts *gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" || issueID != testIssue.ID {
			return nil, fmt.Errorf("error expected")
		}
		if *opts.Title != "" {
			testIssue.Title = *opts.Title
		}
		if *opts.Description != "" {
			testIssue.Description = *opts.Description
		}
		if opts.AddLabels != nil {
			testIssue.Labels = gitlab.Labels(*opts.AddLabels)
		}
		return testIssue, nil
	}

	testCases := []struct {
		Name        string
		Issue       string
		ExpectedMsg []string
		wantErr     bool
	}{
		{
			Name:  "Issue Exists",
			Issue: fmt.Sprintf(`-R %s/cli-automated-testing/test 1 -t "New Title" -d "A new description" --lock-discussion -l newLabel --unlabel bug`, glTestHost),
			ExpectedMsg: []string{
				"- Updating issue #1",
				"✓ updated title to \"New Title\"",
				"✓ locked discussion",
				"✓ added labels newLabel",
				"✓ removed labels bug",
				"#1 New Title",
			},
		},
		{
			Name:        "Issue Does Not Exist",
			Issue:       "0",
			ExpectedMsg: []string{"- Updating issue #0", "error expected"},
			wantErr:     true,
		},
		{
			Name:  "Due date applied",
			Issue: fmt.Sprintf(`-R %s/cli-automated-testing/test 1 -t "New Title" --due-date 2025-07-22`, glTestHost),
			ExpectedMsg: []string{
				"- Updating issue #1",
				"✓ updated title to \"New Title\"",
				"✓ added due date",
				"#1 New Title",
			},
		},
	}

	cfg, err := config.Init()
	require.NoError(t, err)
	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdutils.NewFactory(ios, false, cfg, api.BuildInfo{})

	cmd := NewCmdUpdate(f)
	cmdutils.EnableRepoOverride(cmd, f)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			args, _ := shlex.Split(tc.Issue)
			cmd.SetArgs(args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			_, err := cmd.ExecuteC()
			if tc.wantErr {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}

			out := stripansi.Strip(stdout.String())
			outErr := stripansi.Strip(stderr.String())

			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out, msg)
				assert.Contains(t, outErr, "")
			}
		})
	}

	api.UpdateIssue = oldUpdateIssue
}
