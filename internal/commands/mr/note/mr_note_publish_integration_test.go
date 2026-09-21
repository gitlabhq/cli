//go:build integration

package note

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func Test_MrNotePublish_Integration(t *testing.T) {
	glTestHost := test.GetHostOrSkip(t)

	const projectPath = "cli-automated-testing/test"

	fixtureClient, err := gitlab.NewClient(os.Getenv("GITLAB_TOKEN_TEST"), gitlab.WithBaseURL(glTestHost+"/api/v4"))
	require.NoError(t, err)

	project, _, err := fixtureClient.Projects.GetProject(projectPath, nil)
	require.NoError(t, err)

	branchName := fmt.Sprintf("glab-publish-it-%d", time.Now().Unix())
	_, _, err = fixtureClient.Branches.CreateBranch(projectPath, &gitlab.CreateBranchOptions{
		Branch: &branchName,
		Ref:    &project.DefaultBranch,
	})
	require.NoError(t, err)
	defer fixtureClient.Branches.DeleteBranch(projectPath, branchName)

	_, _, err = fixtureClient.Commits.CreateCommit(projectPath, &gitlab.CreateCommitOptions{
		Branch:        &branchName,
		CommitMessage: new("test: add publish integration fixture file"),
		Actions: []*gitlab.CommitActionOptions{{
			Action:   new(gitlab.FileCreate),
			FilePath: new(fmt.Sprintf("publish-it-%d.txt", time.Now().Unix())),
			Content:  new("publish integration fixture\n"),
		}},
	})
	require.NoError(t, err)

	mr, _, err := fixtureClient.MergeRequests.CreateMergeRequest(projectPath, &gitlab.CreateMergeRequestOptions{
		Title:        new("Draft: publish integration fixture " + branchName),
		SourceBranch: &branchName,
		TargetBranch: &project.DefaultBranch,
	})
	require.NoError(t, err)
	defer fixtureClient.MergeRequests.UpdateMergeRequest(projectPath, mr.IID, &gitlab.UpdateMergeRequestOptions{
		StateEvent: new("close"),
	})

	cfg, err := config.Init()
	require.NoError(t, err)

	exec := func(cmdFunc cmdtest.CmdFunc, cli string) (string, string, error) {
		ios, _, stdout, stderr := cmdtest.TestIOStreams()
		f := cmdutils.NewFactory(ios, false, cfg, api.BuildInfo{})
		cmd := cmdFunc(f)
		cmdutils.EnableRepoOverride(cmd, f)
		_, err := cmdtest.ExecuteCommand(cmd, cli, stdout, stderr)
		return stdout.String(), stderr.String(), err
	}

	mrArg := fmt.Sprintf("%d -R %s", mr.IID, projectPath)

	// Create one pending review comment; stdout is the bare draft ID.
	stdout, _, err := exec(NewCmdCreate, fmt.Sprintf(`%s --draft -m "it draft"`, mrArg))
	require.NoError(t, err)
	_, err = strconv.ParseInt(strings.TrimSpace(stdout), 10, 64)
	require.NoError(t, err, "expected bare numeric draft ID, got %q", stdout)

	// Non-interactive publish without --yes is rejected.
	_, _, err = exec(NewCmdPublish, mrArg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes required when not running interactively")

	stdout, _, err = exec(NewCmdPublish, fmt.Sprintf(`%s -y -m "it summary" --reviewer-state reviewed`, mrArg))
	require.NoError(t, err)
	assert.Contains(t, stdout, "✓ Published 1 pending review comment.")

	drafts, _, err := fixtureClient.DraftNotes.ListDraftNotes(projectPath, mr.IID, &gitlab.ListDraftNotesOptions{})
	require.NoError(t, err)
	assert.Empty(t, drafts)

	notes, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Note, *gitlab.Response, error) {
		return fixtureClient.Notes.ListMergeRequestNotes(projectPath, mr.IID, &gitlab.ListMergeRequestNotesOptions{}, p)
	})
	require.NoError(t, err)
	bodies := make([]string, 0, len(notes))
	for _, n := range notes {
		bodies = append(bodies, n.Body)
	}
	assert.Contains(t, strings.Join(bodies, "\n"), "it draft")
	assert.Contains(t, strings.Join(bodies, "\n"), "it summary")

	_, _, err = exec(NewCmdPublish, mrArg+" -y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pending review comments")
}
