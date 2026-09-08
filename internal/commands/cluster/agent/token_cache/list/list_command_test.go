package list

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// helper to write a filesystem cached token under <cacheDir>/gitlab/<base64(url)>-<agentID>
func writeFSToken(t *testing.T, gitlabURL string, agentID int64, pat *gitlab.PersonalAccessToken) {
	t.Helper()

	// Use os.UserCacheDir to get the actual cache directory that the code will use
	// This ensures compatibility across Windows, Linux, and macOS
	actualCacheDir, err := os.UserCacheDir()
	require.NoError(t, err)

	enc := base64.StdEncoding.EncodeToString([]byte(gitlabURL))
	fname := enc + "-" + strconv.FormatInt(agentID, 10)
	dir := filepath.Join(actualCacheDir, "gitlab")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	fp := filepath.Join(dir, fname)
	data, err := json.Marshal(pat)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fp, data, 0o600))
}

// setUserCacheDir points Go's UserCacheDir to tempDir for this process (Windows, Unix, and macOS)
func setUserCacheDir(t *testing.T, tempDir string) {
	t.Helper()
	t.Setenv("LocalAppData", tempDir)   // Windows
	t.Setenv("LOCALAPPDATA", tempDir)   // Windows
	t.Setenv("XDG_CACHE_HOME", tempDir) // Unix (Linux, BSD, etc.)
	t.Setenv("HOME", tempDir)           // macOS (uses $HOME/Library/Caches)
}

func TestList_NoTokens_Default(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("--filesystem --keyring=false")
	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`
        No cached tokens found.
    `), out.String())
	assert.Empty(t, out.Stderr())
}

func TestList_FilesystemTokens_ShowsTable(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	// create a temp cache directory
	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	expires := new(gitlab.ISOTime(time.Now().Add(1 * time.Hour)))
	pat := &gitlab.PersonalAccessToken{Name: "tok1", ExpiresAt: expires}
	writeFSToken(t, tc.Client.BaseURL().String(), 7, pat)

	out, err := exec("--filesystem --keyring=false")
	require.NoError(t, err)
	// token table should contain agent id and token name
	assert.Contains(t, out.String(), "Agent ID")
	assert.Contains(t, out.String(), "7")
	assert.Contains(t, out.String(), "tok1")
	assert.Empty(t, out.Stderr())
}

func TestList_FilterAgents(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))
	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	pat := &gitlab.PersonalAccessToken{Name: "tokA"}
	writeFSToken(t, tc.Client.BaseURL().String(), 10, pat)
	writeFSToken(t, tc.Client.BaseURL().String(), 11, pat)

	out, err := exec("--filesystem --keyring=false --agent 10")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "10")
	assert.NotContains(t, out.String(), "11")
}
