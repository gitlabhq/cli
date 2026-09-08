//go:build !integration

package create

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

// Differs from the repo the merge request is created against, so the tests can
// tell which one the upload used.
const attachTargetProject = "UPSTREAM/REPO"

func writeAttachFixture(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))
	return path
}

func attachFactoryOptions(t *testing.T, testClient *gitlabtesting.TestClient) []cmdtest.FactoryOption {
	t.Helper()

	cs, csTeardown := test.InitCmdStubber()
	t.Cleanup(csTeardown)
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	return []cmdtest.FactoryOption{
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{Name: "upstream", Resolved: "head", PushURL: pu},
						Repo:   glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{Name: "origin", Resolved: "base", PushURL: pu},
						Repo:   glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) { return "feat-new-mr", nil }
		},
	}
}

func expectProjectForAttach(testClient *gitlabtesting.TestClient) {
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    attachTargetProject,
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		ListProjectTargetBranchRules(attachTargetProject, gomock.Any()).
		Return([]gitlab.TargetBranchRule{}, nil, nil)
}

func TestMRCreate_AttachUploadsToTargetProjectNotHeadRepo(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	path := writeAttachFixture(t, "screenshot.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectProjectForAttach(testClient)

	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown(attachTargetProject, gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	var gotDescription *string
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					IID:    12,
					WebURL: "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, attachFactoryOptions(t, testClient)...)

	_, err := exec(`-t myMRtitle -d "Look at this." --attach ` + path + ` --yes`)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "Look at this.\n\n![screenshot](/uploads/abc/screenshot.png)", *gotDescription)
}

func TestMRCreate_AttachWithoutDescription(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	path := writeAttachFixture(t, "screenshot.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectProjectForAttach(testClient)
	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown(attachTargetProject, gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	var gotDescription *string
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{IID: 12, WebURL: "https://gitlab.com/OWNER/REPO/-/merge_requests/12"},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, attachFactoryOptions(t, testClient)...)

	_, err := exec(`-t myMRtitle --attach ` + path + ` --yes`)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "![screenshot](/uploads/abc/screenshot.png)", *gotDescription)
}

func TestMRCreate_NoAttachLeavesTheDescriptionAlone(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)
	expectProjectForAttach(testClient)

	var gotDescription *string
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{IID: 12, WebURL: "https://gitlab.com/OWNER/REPO/-/merge_requests/12"},
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, attachFactoryOptions(t, testClient)...)

	_, err := exec(`-t myMRtitle -d "Look at this." --yes`)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "Look at this.", *gotDescription)
}

func TestGenerateMRCompareURL_IncludesAttachmentReferences(t *testing.T) {
	opts := &options{
		body:          "Look at this.\n\n![screenshot](/uploads/abc/screenshot.png)",
		SourceBranch:  "feat-new-mr",
		TargetBranch:  "master",
		Title:         "myMRtitle",
		TargetProject: &gitlab.Project{ID: 100},
		SourceProject: &gitlab.Project{
			ID:     101,
			WebURL: "https://gitlab.com/OWNER/REPO",
		},
	}

	u, err := generateMRCompareURL(opts)
	require.NoError(t, err)
	assert.Contains(t, u, url.QueryEscape("![screenshot](/uploads/abc/screenshot.png)"))
}
