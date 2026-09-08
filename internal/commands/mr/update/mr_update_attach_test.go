//go:build !integration

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func writeAttachFixture(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))
	return path
}

// stubMRAPI returns an accessor for the description that reached the update.
func stubMRAPI(t *testing.T, existingDescription string) func() *string {
	t.Helper()

	oldUpdateMR, oldGetMR := api.UpdateMR, api.GetMR
	t.Cleanup(func() { api.UpdateMR, api.GetMR = oldUpdateMR, oldGetMR })

	api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
		return &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID: mrID, IID: mrID, Title: "mrTitle", State: "opened", Description: existingDescription,
		}}, nil
	}

	var gotDescription *string
	api.UpdateMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
		gotDescription = opts.Description
		return &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: mrID, IID: mrID, Title: "mrTitle", State: "opened"}}, nil
	}

	return func() *string { return gotDescription }
}

func TestUpdateMergeRequest_AttachAppendsToTheExistingBody(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	description := stubMRAPI(t, "Original body.")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`1 --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Original body.\n\n![screenshot](/uploads/abc/screenshot.png)", *description())
}

func TestUpdateMergeRequest_AttachWithDescriptionReplacesTheBody(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	description := stubMRAPI(t, "Original body.")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`1 --description "Replaced body." --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Replaced body.\n\n![screenshot](/uploads/abc/screenshot.png)", *description())
}

func TestUpdateMergeRequest_NoAttachLeavesTheBodyAlone(t *testing.T) {
	description := stubMRAPI(t, "Original body.")

	exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false,
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`1 --description "Replaced body."`)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Replaced body.", *description())
}
