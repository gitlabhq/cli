//go:build !integration

package semantic

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestSemanticSearch_MissingQuery(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "required flag(s) \"query\" not set")
}

func TestSemanticSearch_TextOutput(t *testing.T) {
	t.Parallel()

	response := semanticSearchResponse{
		Confidence: "high",
		Results: []searchResult{
			{
				Path:    "app/services/auth.rb",
				FileURL: "https://gitlab.com/owner/repo/-/blob/main/app/services/auth.rb",
				Score:   0.94,
				SnippetRanges: []codeChunk{
					{StartLine: 10, EndLine: 20, Content: "def authenticate\n  ...\nend"},
				},
			},
		},
	}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "authentication middleware", r.URL.Query().Get("q"))
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "search/semantic")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(gitlabClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec(`-q "authentication middleware"`)
	require.NoError(t, err)

	out := output.String()
	assert.Contains(t, out, "authentication middleware")
	assert.Contains(t, out, "OWNER/REPO")
	assert.Contains(t, out, "Confidence: high")
	assert.Contains(t, out, "app/services/auth.rb")
	assert.Contains(t, out, "0.94")
	assert.Contains(t, out, "def authenticate")
}

func TestSemanticSearch_JSONOutput(t *testing.T) {
	t.Parallel()

	response := semanticSearchResponse{
		Confidence: "medium",
		Results: []searchResult{
			{
				Path:          "lib/foo.rb",
				FileURL:       "https://gitlab.com/owner/repo/-/blob/main/lib/foo.rb",
				Score:         0.75,
				SnippetRanges: []codeChunk{{StartLine: 1, EndLine: 5, Content: "# foo"}},
			},
		},
	}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(gitlabClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec(`-q foo --output json`)
	require.NoError(t, err)

	var got semanticSearchResponse
	err = json.Unmarshal([]byte(output.String()), &got)
	require.NoError(t, err)
	assert.Equal(t, "medium", got.Confidence)
	assert.Len(t, got.Results, 1)
	assert.Equal(t, "lib/foo.rb", got.Results[0].Path)
}

func TestSemanticSearch_WithDirectoryPath(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "app/services/", r.URL.Query().Get("directory_path"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(semanticSearchResponse{Confidence: "low", Results: nil})
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(gitlabClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err = exec(`-q "rate limiting" -d app/services/`)
	require.NoError(t, err)
}

func TestSemanticSearch_NoResults(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(semanticSearchResponse{Confidence: "high", Results: nil})
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(gitlabClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec(`-q "nonexistent code"`)
	require.NoError(t, err)

	assert.Contains(t, output.String(), "No results found.")
}

func TestSemanticSearch_KnnOutOfRange(t *testing.T) {
	t.Parallel()

	for _, cli := range []string{`-q foo --knn=-1`, `-q foo --knn=101`} {
		exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)
		_, err := exec(cli)
		require.Error(t, err, "expected error for %q", cli)
		assert.Contains(t, err.Error(), "--knn must be between 1 and 100")
	}
}

func TestSemanticSearch_LimitOutOfRange(t *testing.T) {
	t.Parallel()

	for _, cli := range []string{`-q foo --limit=-1`, `-q foo --limit=101`} {
		exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)
		_, err := exec(cli)
		require.Error(t, err, "expected error for %q", cli)
		assert.Contains(t, err.Error(), "--limit must be between 1 and 100")
	}
}

func TestSemanticSearch_BaseRepoError(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBaseRepoError(errors.New("not a git repository")),
	)

	_, err := exec(`-q foo`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestSemanticSearch_APIError(t *testing.T) {
	t.Parallel()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"No embeddings indexed for this project"}`))
	}))
	defer testServer.Close()

	gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(gitlabClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err = exec(`-q foo`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "semantic search request failed")
}
