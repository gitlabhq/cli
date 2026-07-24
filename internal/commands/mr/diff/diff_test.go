//go:build !integration

package diff

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdDiff(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		isTTY   bool
		want    options
		wantErr string
	}{
		{
			name:  "number argument",
			args:  "123",
			isTTY: true,
			want: options{
				args:     []string{"123"},
				useColor: "auto",
			},
		},
		{
			name:  "no argument",
			args:  "",
			isTTY: true,
			want: options{
				useColor: "auto",
			},
		},
		{
			name:  "no color when redirected",
			args:  "",
			isTTY: false,
			want: options{
				useColor: "never",
			},
		},
		{
			name:    "no argument with --repo override",
			args:    "-R owner/repo",
			isTTY:   true,
			wantErr: "argument required when using the --repo flag",
		},
		{
			name:    "invalid --color argument",
			args:    "--color doublerainbow",
			isTTY:   true,
			wantErr: `did not understand color "doublerainbow": expected one of 'always', 'never', or 'auto'`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(tt.isTTY))

			f := cmdtest.NewTestFactory(ios)

			var opts *options
			cmd := NewCmdDiff(f, func(o *options) error {
				opts = o
				return nil
			})
			cmd.PersistentFlags().StringP("repo", "R", "", "")

			argv, err := shlex.Split(tt.args)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.want.args, opts.args)
			assert.Equal(t, tt.want.useColor, opts.useColor)
		})
	}
}

// newCmdDiffWrapper wraps NewCmdDiff to match cmdtest.CmdFunc signature
func newCmdDiffWrapper(f cmdutils.Factory) *cobra.Command {
	return NewCmdDiff(f, nil)
}

func TestMRDiff_raw(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetMergeRequest
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test1",
				Description: "fixed login page css paddings",
				State:       "merged",
			},
		}, nil, nil)

	rawDiff := heredoc.Doc(`
	diff --git a/file.txt b/file.txt
	index 123..456 100644
	--- a/file.txt
	+++ b/file.txt
	@@ -1 +1 @@
	-old line
	+new line`)

	// Mock ShowMergeRequestRawDiffs
	testClient.MockMergeRequests.EXPECT().
		ShowMergeRequestRawDiffs("OWNER/REPO", int64(123), gomock.Any()).
		Return([]byte(rawDiff), nil, nil)

	exec := cmdtest.SetupCmdForTest(t, newCmdDiffWrapper, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("123 --raw")
	require.NoError(t, err)
	assert.Equal(t, rawDiff, output.String())
	assert.Empty(t, output.Stderr())
}

func TestMRDiff_no_current_mr(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	// Mock ListProjectMergeRequests - returns empty list (no MR for branch)
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, newCmdDiffWrapper, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("feature"),
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Equal(t, `no open merge request available for "feature"`, err.Error())
}

func TestMRDiff_argument_not_found(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetMergeRequest
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test1",
				Description: "fixed login page css paddings",
				State:       "merged",
			},
		}, nil, nil)

	// Mock GetMergeRequestDiffVersions - returns 404
	notFoundResp := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusNotFound},
	}
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequestDiffVersions("OWNER/REPO", int64(123), gomock.Any()).
		Return(nil, notFoundResp, gitlab.ErrNotFound)

	exec := cmdtest.SetupCmdForTest(t, newCmdDiffWrapper, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("123")
	require.Error(t, err)
	assert.Empty(t, output.String())
	assert.Empty(t, output.Stderr())
	assert.Contains(t, err.Error(), "could not find merge request diffs")
}

func TestMRDiff_notty(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	// Mock ListProjectMergeRequests - find MR by branch
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test1",
			Description: "fixed login page css paddings",
			State:       "merged",
		}}, nil, nil)

	// Mock GetMergeRequest
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test1",
				Description: "fixed login page css paddings",
				State:       "merged",
			},
		}, nil, nil)

	// Mock GetMergeRequestDiffVersions
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequestDiffVersions("OWNER/REPO", int64(123), gomock.Any()).
		Return([]*gitlab.MergeRequestDiffVersion{
			{
				ID:             110,
				HeadCommitSHA:  "33e2ee8579fda5bc36accc9c6fbd0b4fefda9e30",
				BaseCommitSHA:  "eeb57dffe83deb686a60a71c16c32f71046868fd",
				StartCommitSHA: "eeb57dffe83deb686a60a71c16c32f71046868fd",
				State:          "collected",
				RealSize:       "1",
			},
		}, nil, nil)

	// Mock GetSingleMergeRequestDiffVersion
	testClient.MockMergeRequests.EXPECT().
		GetSingleMergeRequestDiffVersion("OWNER/REPO", int64(123), int64(110), gomock.Any()).
		Return(&gitlab.MergeRequestDiffVersion{
			ID:             110,
			HeadCommitSHA:  "33e2ee8579fda5bc36accc9c6fbd0b4fefda9e30",
			BaseCommitSHA:  "eeb57dffe83deb686a60a71c16c32f71046868fd",
			StartCommitSHA: "eeb57dffe83deb686a60a71c16c32f71046868fd",
			State:          "collected",
			RealSize:       "1",
			Diffs: []*gitlab.Diff{{
				OldPath:     "LICENSE.md",
				NewPath:     "LICENSE",
				AMode:       "0",
				BMode:       "100644",
				Diff:        "@@ -0,0 +1,21 @@\n+The MIT License (MIT)\n+\n+Copyright (c) 2018 Administrator\n+\n+Permission is hereby granted, free of charge, to any person obtaining a copy\n+of this software and associated documentation files (the \"Software\"), to deal\n+in the Software without restriction, including without limitation the rights\n+to use, copy, modify, merge, publish, distribute, sublicense, and/or sell\n+copies of the Software, and to permit persons to whom the Software is\n+furnished to do so, subject to the following conditions:\n+\n+The above copyright notice and this permission notice shall be included in all\n+copies or substantial portions of the Software.\n+\n+THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR\n+IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,\n+FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE\n+AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER\n+LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,\n+OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE\n+SOFTWARE.\n",
				NewFile:     true,
				RenamedFile: true,
				DeletedFile: false,
			}},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, newCmdDiffWrapper, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("feature"),
	)

	output, err := exec("")
	require.NoError(t, err)
	assert.Contains(t, output.String(), "+The MIT License (MIT)")
	assert.Contains(t, output.String(), "+FITNESS")
}

func TestMRDiff_tty(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	longLine := "+" + strings.Repeat("x", 70*1024)

	// Mock ListProjectMergeRequests - find MR by branch
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test1",
			Description: "fixed login page css paddings",
			State:       "merged",
		}}, nil, nil)

	// Mock GetMergeRequest
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test1",
				Description: "fixed login page css paddings",
				State:       "merged",
			},
		}, nil, nil)

	// Mock GetMergeRequestDiffVersions
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequestDiffVersions("OWNER/REPO", int64(123), gomock.Any()).
		Return([]*gitlab.MergeRequestDiffVersion{
			{
				ID:             110,
				HeadCommitSHA:  "33e2ee8579fda5bc36accc9c6fbd0b4fefda9e30",
				BaseCommitSHA:  "eeb57dffe83deb686a60a71c16c32f71046868fd",
				StartCommitSHA: "eeb57dffe83deb686a60a71c16c32f71046868fd",
				State:          "collected",
				RealSize:       "1",
			},
		}, nil, nil)

	// Mock GetSingleMergeRequestDiffVersion
	testClient.MockMergeRequests.EXPECT().
		GetSingleMergeRequestDiffVersion("OWNER/REPO", int64(123), int64(110), gomock.Any()).
		Return(&gitlab.MergeRequestDiffVersion{
			ID:             110,
			HeadCommitSHA:  "33e2ee8579fda5bc36accc9c6fbd0b4fefda9e30",
			BaseCommitSHA:  "eeb57dffe83deb686a60a71c16c32f71046868fd",
			StartCommitSHA: "eeb57dffe83deb686a60a71c16c32f71046868fd",
			State:          "collected",
			RealSize:       "1",
			Diffs: []*gitlab.Diff{{
				OldPath:     "LICENSE.md",
				NewPath:     "LICENSE",
				AMode:       "0",
				BMode:       "100644",
				Diff:        "@@ -0,0 +1,2 @@\n+The MIT License (MIT)\n" + longLine + "\n",
				NewFile:     true,
				RenamedFile: true,
				DeletedFile: false,
			}},
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, newCmdDiffWrapper, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("feature"),
	)

	output, err := exec("")
	require.NoError(t, err)
	// TTY output should have color codes
	assert.Contains(t, output.String(), "\x1b[32m+The MIT License")
	assert.Contains(t, output.String(), longLine)
}

func TestMRDiff_no_diffs_found(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	// Mock ListProjectMergeRequests - find MR by branch
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{{
			ID:          123,
			IID:         123,
			ProjectID:   3,
			Title:       "test1",
			Description: "fixed login page css paddings",
			State:       "merged",
		}}, nil, nil)

	// Mock GetMergeRequest
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          123,
				IID:         123,
				ProjectID:   3,
				Title:       "test1",
				Description: "fixed login page css paddings",
				State:       "merged",
			},
		}, nil, nil)

	// Mock GetMergeRequestDiffVersions - returns empty list
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequestDiffVersions("OWNER/REPO", int64(123), gomock.Any()).
		Return([]*gitlab.MergeRequestDiffVersion{}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, newCmdDiffWrapper, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("feature"),
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Equal(t, "no merge request diffs found", err.Error())
}
