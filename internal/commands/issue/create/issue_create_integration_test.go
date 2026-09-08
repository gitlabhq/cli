//go:build integration

package create

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/utils"
	"gitlab.com/gitlab-org/cli/test"
)

func Test_IssueCreate_Integration(t *testing.T) {
	glTestHost := test.GetHostOrSkip(t)

	cmdtest.CopyTestRepo(t, "issue_create")

	oldCreateIssue := createIssue
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	createIssue = func(client *gitlab.Client, projectID any, opts *gitlab.CreateIssueOptions) (*gitlab.Issue, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" {
			return nil, fmt.Errorf("error expected")
		}
		return &gitlab.Issue{
			ID:          1,
			IID:         1,
			Title:       *opts.Title,
			Labels:      gitlab.Labels(*opts.Labels),
			State:       "opened",
			Description: *opts.Description,
			Weight:      *opts.Weight,
			Author: &gitlab.IssueAuthor{
				ID:       1,
				Name:     "John Dev Wick",
				Username: "jdwick",
			},
			WebURL:    glTestHost + "/cli-automated-testing/test/-/issues/1",
			CreatedAt: &timer,
		}, nil
	}

	cfg, err := config.Init()
	require.NoError(t, err)
	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdutils.NewFactory(ios, false, cfg, api.BuildInfo{})

	cmd := NewCmdCreate(f)
	cmdutils.EnableRepoOverride(cmd, f)

	cliStr := []string{
		"-t", "myissuetitle",
		"-d", "myissuebody",
		"-l", "test,bug",
		"--weight", "1",
		"--milestone", "1",
		"--linked-mr", "3",
		"--confidential",
		"--assignee", "testuser",
		"-R", "cli-automated-testing/test",
	}

	cli := strings.Join(cliStr, " ")
	_, err = cmdtest.ExecuteCommand(cmd, cli, stdout, stderr)
	assert.Nil(t, err)

	out := stripansi.Strip(stdout.String())
	outErr := stripansi.Strip(stderr.String())
	expectedOut := fmt.Sprintf("#1 myissuetitle (%s)", utils.TimeToPrettyTimeAgo(timer))
	outputLines := strings.SplitN(out, "\n", 2)
	assert.Contains(t, outputLines[0], expectedOut)
	assert.Equal(t, expectedOut, outputLines[0])
	assert.Equal(t, "- Creating issue in cli-automated-testing/test\n", outErr)
	assert.Contains(t, out, glTestHost+"/cli-automated-testing/test/-/issues/1")

	createIssue = oldCreateIssue
}

func Test_IssueCreate_With_Recover_Integration(t *testing.T) {
	glTestHost := test.GetHostOrSkip(t)

	cmdtest.CopyTestRepo(t, "issue_create_with_recover")

	oldCreateIssue := createIssue
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	createIssue = func(client *gitlab.Client, projectID any, opts *gitlab.CreateIssueOptions) (*gitlab.Issue, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" {
			return nil, fmt.Errorf("error expected")
		}
		return &gitlab.Issue{
			ID:          1,
			IID:         1,
			Title:       *opts.Title,
			Labels:      gitlab.Labels(*opts.Labels),
			State:       "opened",
			Description: *opts.Description,
			Weight:      *opts.Weight,
			Author: &gitlab.IssueAuthor{
				ID:       1,
				Name:     "John Dev Wick",
				Username: "jdwick",
			},
			WebURL:    glTestHost + "/cli-automated-testing/test/-/issues/2",
			CreatedAt: &timer,
		}, nil
	}

	cfg, err := config.Init()
	require.NoError(t, err)
	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdutils.NewFactory(ios, false, cfg, api.BuildInfo{})

	oldCreateRun := createRun

	// Force createRun to throw error
	createRun = func(_ context.Context, opts *options) error {
		return errors.New("fail on purpose")
	}

	cmd := NewCmdCreate(f)
	cmdutils.EnableRepoOverride(cmd, f)

	cliStr := []string{
		"-t", "myissuetitle",
		"-d", "myissuebody",
		"-l", "test,bug",
		"--weight", "1",
		"--milestone", "1",
		"--linked-mr", "3",
		"--confidential",
		"--assignee", "testuser",
		"-R", "cli-automated-testing/test",
	}

	cli := strings.Join(cliStr, " ")
	_, err = cmdtest.ExecuteCommand(cmd, cli, stdout, stderr)
	assert.Contains(t, err.Error(), "fail on purpose")

	out := stripansi.Strip(stdout.String())
	outErr := stripansi.Strip(stderr.String())

	assert.Contains(t, outErr, "Failed to create issue. Created recovery file: ")
	assert.Empty(t, out)

	// Revert to original state
	createRun = oldCreateRun

	// Run create issue with recover
	newCliStr := append(cliStr, "--recover")

	stdout.Reset()
	stderr.Reset()

	newcli := strings.Join(newCliStr, " ")

	_, newerr := cmdtest.ExecuteCommand(cmd, newcli, stdout, stderr)
	assert.Nil(t, newerr)

	newout := stripansi.Strip(stdout.String())
	newoutErr := stripansi.Strip(stderr.String())
	expectedOut := fmt.Sprintf("#1 myissuetitle (%s)", utils.TimeToPrettyTimeAgo(timer))

	assert.Contains(t, newout, expectedOut)
	assert.Contains(t, newout, "Recovered create options from file.")
	assert.Equal(t, "- Creating issue in cli-automated-testing/test\n", newoutErr)
	assert.Contains(t, newout, glTestHost+"/cli-automated-testing/test/-/issues/2")

	createIssue = oldCreateIssue
}
