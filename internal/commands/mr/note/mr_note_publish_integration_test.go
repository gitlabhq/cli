//go:build integration

package note

import (
	"fmt"
	"os"
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

// Test_MrNotePublish_Integration checks the one thing a mock cannot: that a real
// instance applies what the deprecated PublishAllDraftNotesWithOptions call sends,
// rather than merely accepting it, and that the drafts really are consumed.
// Flag handling, output, and error wrapping are covered in mr_note_publish_test.go.
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
	t.Cleanup(func() {
		if _, err := fixtureClient.Branches.DeleteBranch(projectPath, branchName); err != nil {
			t.Logf("cleanup: deleting branch %s: %v", branchName, err)
		}
	})

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
	t.Cleanup(func() {
		_, _, err := fixtureClient.MergeRequests.UpdateMergeRequest(projectPath, mr.IID, &gitlab.UpdateMergeRequestOptions{
			StateEvent: new("close"),
		})
		if err != nil {
			t.Logf("cleanup: closing !%d: %v", mr.IID, err)
		}
	})

	cfg, err := config.Init()
	require.NoError(t, err)

	exec := func(cmdFunc cmdtest.CmdFunc, cli string) error {
		ios, _, stdout, stderr := cmdtest.TestIOStreams()
		f := cmdutils.NewFactory(ios, false, cfg, api.BuildInfo{})
		cmd := cmdFunc(f)
		cmdutils.EnableRepoOverride(cmd, f)
		_, err := cmdtest.ExecuteCommand(cmd, cli, stdout, stderr)
		return err
	}

	mrArg := fmt.Sprintf("%d -R %s", mr.IID, projectPath)

	require.NoError(t, exec(NewCmdCreate, fmt.Sprintf(`%s --draft -m "it draft"`, mrArg)))

	// Publishing consumes every draft, so this one call carries every body field
	// the assertions below can check. --reviewer-state is deliberately absent:
	// bulk_publish discards the UpdateReviewerStateService result, and that
	// service refuses to create a reviewer row for a merge request's own author,
	// which the fixture user always is. Sending it here would assert nothing.
	require.NoError(t, exec(NewCmdPublish, fmt.Sprintf(`%s -y -m "it summary" --internal`, mrArg)))

	drafts, _, err := fixtureClient.DraftNotes.ListDraftNotes(projectPath, mr.IID, &gitlab.ListDraftNotesOptions{})
	require.NoError(t, err)
	assert.Empty(t, drafts)

	notes, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Note, *gitlab.Response, error) {
		return fixtureClient.Notes.ListMergeRequestNotes(projectPath, mr.IID, &gitlab.ListMergeRequestNotesOptions{}, p)
	})
	require.NoError(t, err)

	var published, summary *gitlab.Note
	for _, n := range notes {
		switch strings.TrimSpace(n.Body) {
		case "it draft":
			published = n
		case "it summary":
			summary = n
		}
	}
	require.NotNil(t, published, "published draft note missing from !%d", mr.IID)
	require.NotNil(t, summary, "summary note missing from !%d", mr.IID)

	// The draft is the control: if both came back internal, the flag would not be
	// what made the summary internal.
	assert.True(t, summary.Internal, "--internal should have marked the summary note internal")
	assert.False(t, published.Internal, "the published draft should not be internal")
}
