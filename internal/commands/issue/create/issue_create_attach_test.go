//go:build !integration

package create

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func writeAttachFixture(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))
	return path
}

func expectProjectForAttach(testClient *gitlabtesting.TestClient) {
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                37777023,
			PathWithNamespace: "OWNER/REPO",
			IssuesEnabled:     true,
		}, nil, nil)
}

func TestIssueCreate_AttachAppendsReferenceToDescription(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectProjectForAttach(testClient)
	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	var gotDescription *string
	testClient.MockIssues.EXPECT().
		CreateIssue("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.Issue{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/issues/1"}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, cmdtest.WithGitLabClient(testClient.Client))

	_, err := exec(`--title "Login button misaligned" --description "See below." --attach ` + path + ` --yes`)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "See below.\n\n![screenshot](/uploads/abc/screenshot.png)", *gotDescription)
}

func TestIssueCreate_AttachSatisfiesNonInteractiveMode(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectProjectForAttach(testClient)
	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	var gotDescription *string
	testClient.MockIssues.EXPECT().
		CreateIssue("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.Issue{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/issues/1"}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, cmdtest.WithGitLabClient(testClient.Client))

	_, err := exec(`--title "Login button misaligned" --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "![screenshot](/uploads/abc/screenshot.png)", *gotDescription)
}

func TestIssueCreate_NonInteractiveErrorMentionsAttach(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false)

	_, err := exec(`--title "no body anywhere"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--attach")
}

func TestIssueCreate_NoAttachLeavesTheDescriptionAlone(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	expectProjectForAttach(testClient)

	var gotDescription *string
	testClient.MockIssues.EXPECT().
		CreateIssue("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.Issue{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/issues/1"}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, cmdtest.WithGitLabClient(testClient.Client))

	_, err := exec(`--title "Login button misaligned" --description "See below." --yes`)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "See below.", *gotDescription)
}

func TestGenerateIssueWebURL_IncludesAttachmentReferences(t *testing.T) {
	opts := &options{
		body:  "See below.\n\n![screenshot](/uploads/abc/screenshot.png)",
		Title: "Login button misaligned",
		baseProject: &gitlab.Project{
			ID:     101,
			WebURL: "https://gitlab.example.com/gitlab-org/gitlab",
		},
	}

	u, err := generateIssueWebURL(opts)
	require.NoError(t, err)
	assert.Contains(t, u, "%21%5Bscreenshot%5D%28%2Fuploads%2Fabc%2Fscreenshot.png%29")
}
