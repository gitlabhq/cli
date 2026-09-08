//go:build !integration

package create

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestSnippetCreate(t *testing.T) {
	type testCase struct {
		name       string
		command    string
		wantErr    string
		wantStderr []string
		wantStdout []string
		setupMock  func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:       "Create personal snippet",
			command:    "testdata/snippet.txt --personal -d 'Hello World snippet' -f 'snippet.txt' -t 'This is a snippet'",
			wantStderr: []string{"- Creating snippet in personal space"},
			wantStdout: []string{"https://gitlab.example.com/-/snippets/1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSnippets.EXPECT().
					CreateSnippet(gomock.Any()).
					Return(&gitlab.Snippet{
						ID:          1,
						Title:       "This is a snippet",
						Description: "Hello World snippet",
						WebURL:      "https://gitlab.example.com/-/snippets/1",
						FileName:    "snippet.txt",
						Files: []gitlab.SnippetFile{
							{
								Path:   "snippet.txt",
								RawURL: "https://gitlab.example.com/-/snippets/1/raw/main/snippet.txt",
							},
						},
					}, nil, nil)
			},
		},
		{
			name:       "Create project snippet",
			command:    "testdata/snippet.txt -d 'Hello World snippet' -f 'snippet.txt' -t 'This is a snippet'",
			wantStderr: []string{"- Creating snippet in OWNER/REPO"},
			wantStdout: []string{"https://gitlab.example.com/OWNER/REPO/-/snippets/1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectSnippets.EXPECT().
					CreateSnippet("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Snippet{
						ID:          1,
						Title:       "This is a snippet",
						Description: "Hello World snippet",
						WebURL:      "https://gitlab.example.com/OWNER/REPO/-/snippets/1",
						FileName:    "snippet.txt",
						Files: []gitlab.SnippetFile{
							{
								Path:   "snippet.txt",
								RawURL: "https://gitlab.example.com/-/OWNER/REPO/snippets/1/raw/main/snippet.txt",
							},
						},
					}, nil, nil)
			},
		},

		{
			name:       "Create project snippet using a path",
			command:    "testdata/snippet.txt -d 'Hello World snippet' -t 'This is a snippet'",
			wantStderr: []string{"- Creating snippet in OWNER/REPO"},
			wantStdout: []string{"https://gitlab.example.com/OWNER/REPO/-/snippets/1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectSnippets.EXPECT().
					CreateSnippet("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Snippet{
						ID:          1,
						Title:       "This is a snippet",
						Description: "Hello World snippet",
						WebURL:      "https://gitlab.example.com/OWNER/REPO/-/snippets/1",
						FileName:    "snippet.txt",
						Files: []gitlab.SnippetFile{
							{
								Path:   "snippet.txt",
								RawURL: "https://gitlab.example.com/-/OWNER/REPO/snippets/1/raw/main/snippet.txt",
							},
						},
					}, nil, nil)
			},
		},

		{
			name:       "Create project snippet from multiple files",
			command:    "-d 'Hello World snippet' -t 'This is a snippet' testdata/file1.md testdata/file2.md",
			wantStderr: []string{"- Creating snippet in OWNER/REPO"},
			wantStdout: []string{"https://gitlab.example.com/OWNER/REPO/-/snippets/1"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectSnippets.EXPECT().
					CreateSnippet("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Snippet{
						ID:          1,
						Title:       "This is a snippet",
						Description: "Hello World snippet",
						WebURL:      "https://gitlab.example.com/OWNER/REPO/-/snippets/1",
						FileName:    "snippet.txt",
						Files: []gitlab.SnippetFile{
							{
								Path:   "snippet.txt",
								RawURL: "https://gitlab.example.com/-/OWNER/REPO/snippets/1/raw/main/snippet.txt",
							},
						},
					}, nil, nil)
			},
		},
		{
			name:    "Create snippet 403 failure",
			command: "testdata/snippet.txt -d 'Hello World snippet' -f 'snippet.txt' -t 'This is a snippet'",
			wantErr: "failed to create snippet: 403 Forbidden",
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockProjectSnippets.EXPECT().
					CreateSnippet("OWNER/REPO", gomock.Any()).
					Return(nil, forbiddenResponse, fmt.Errorf("403 Forbidden"))
			},
		},
		{
			name:    "Create personal snippet 403 failure",
			command: "testdata/snippet.txt --personal -d 'Hello World snippet' -f 'snippet.txt' -t 'This is a personal snippet'",
			wantErr: "failed to create snippet: 403 Forbidden",
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockSnippets.EXPECT().
					CreateSnippet(gomock.Any()).
					Return(nil, forbiddenResponse, fmt.Errorf("403 Forbidden"))
			},
		},
		{
			name:    "Create snippet no stdin failure",
			command: "-d 'Hello World snippet' -f 'snippet.txt' -t 'This is a personal snippet'",
			wantErr: "stdin required if no 'path' is provided",
			setupMock: func(tc *gitlabtesting.TestClient) {
				// No mock needed - fails before API call
			},
		},
		{
			name:    "Create snippet no path failure",
			command: "-d 'Hello World snippet' -t 'This is a personal snippet'",
			wantErr: "if 'path' is not provided, 'filename' and stdin are required",
			setupMock: func(tc *gitlabtesting.TestClient) {
				// No mock needed - fails before API call
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.command)

			// THEN
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)

			for _, msg := range tc.wantStdout {
				assert.Contains(t, out.String(), msg)
			}

			for _, msg := range tc.wantStderr {
				assert.Contains(t, out.Stderr(), msg)
			}
		})
	}
}
