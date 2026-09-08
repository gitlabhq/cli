package clear

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
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// helper to write a filesystem cached token under <cacheDir>/gitlab/<base64(url)>-<agentID>
func writeFSToken(t *testing.T, gitlabURL string, agentID int64, pat *gitlab.PersonalAccessToken) string {
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
	return fp
}

// setUserCacheDir points Go's UserCacheDir to tempDir for this process (Windows, Unix, and macOS)
func setUserCacheDir(t *testing.T, tempDir string) {
	t.Helper()
	t.Setenv("LocalAppData", tempDir)   // Windows
	t.Setenv("LOCALAPPDATA", tempDir)   // Windows
	t.Setenv("XDG_CACHE_HOME", tempDir) // Unix (Linux, BSD, etc.)
	t.Setenv("HOME", tempDir)           // macOS (uses $HOME/Library/Caches)
}

func TestClear_ValidationError_NoSources(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	_, err := exec("--filesystem=false --keyring=false")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one cache source must be enabled")
}

func TestClear_NoTokens_ReportsEmpty(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	out, err := exec("--filesystem --keyring=false --revoke=false")
	require.NoError(t, err)
	assert.Equal(t, heredoc.Doc(`
		No cached tokens found to clear.
	`), out.String())
	assert.Empty(t, out.Stderr())
}

func TestClear_FilesystemTokens_DeletesFiles(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	pat := &gitlab.PersonalAccessToken{ID: 123, Name: "token1"}
	filePath := writeFSToken(t, tc.Client.BaseURL().String(), 5, pat)

	// verify file exists before clear
	_, err := os.Stat(filePath)
	require.NoError(t, err, "token file should exist before clear")

	out, err := exec("--filesystem --keyring=false --revoke=false")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Found 1 cached token(s) to clear")
	assert.Contains(t, out.String(), "Cleared token for agent 5 from filesystem")
	assert.Contains(t, out.String(), "Successfully cleared 1 token(s) from cache")

	// verify file deleted after clear
	_, err = os.Stat(filePath)
	assert.True(t, os.IsNotExist(err), "token file should be deleted after clear")
}

func TestClear_WithRevoke_ActiveToken(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	expires := new(gitlab.ISOTime(time.Now().UTC().Add(24 * time.Hour)))
	pat := &gitlab.PersonalAccessToken{ID: 456, Name: "active-token", ExpiresAt: expires, Revoked: false}
	writeFSToken(t, tc.Client.BaseURL().String(), 10, pat)

	// mock revocation API call
	tc.MockPersonalAccessTokens.EXPECT().
		RevokePersonalAccessTokenByID(int64(456), gomock.Any()).
		Return(&gitlab.Response{}, nil).
		Times(1)

	out, err := exec("--filesystem --keyring=false --revoke=true")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Revoking tokens on GitLab server")
	assert.Contains(t, out.String(), "Revoking token for agent 10")
	assert.Contains(t, out.String(), "Successfully revoked token for agent 10")
	assert.Contains(t, out.String(), "Cleared token for agent 10 from filesystem")
}

func TestClear_WithRevoke_SkipsExpiredToken(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	expires := new(gitlab.ISOTime(time.Now().Add(-24 * time.Hour)))
	pat := &gitlab.PersonalAccessToken{ID: 789, Name: "expired-token", ExpiresAt: expires, Revoked: false}
	writeFSToken(t, tc.Client.BaseURL().String(), 15, pat)

	// no API call expected for expired token
	tc.MockPersonalAccessTokens.EXPECT().
		RevokePersonalAccessTokenByID(gomock.Any(), gomock.Any()).
		Times(0)

	out, err := exec("--filesystem --keyring=false --revoke=true")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Token for agent 15 is expired, skipping revocation")
	assert.Contains(t, out.String(), "Cleared token for agent 15 from filesystem")
}

func TestClear_WithRevoke_SkipsAlreadyRevokedToken(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	pat := &gitlab.PersonalAccessToken{ID: 999, Name: "revoked-token", Revoked: true}
	writeFSToken(t, tc.Client.BaseURL().String(), 20, pat)

	// no API call expected for already revoked token
	tc.MockPersonalAccessTokens.EXPECT().
		RevokePersonalAccessTokenByID(gomock.Any(), gomock.Any()).
		Times(0)

	out, err := exec("--filesystem --keyring=false --revoke=true")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Token for agent 20 is already revoked, skipping")
	assert.Contains(t, out.String(), "Cleared token for agent 20 from filesystem")
}

func TestClear_FilterAgents_OnlySpecified(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	cacheDir := t.TempDir()
	setUserCacheDir(t, cacheDir)

	pat1 := &gitlab.PersonalAccessToken{ID: 100, Name: "token-agent-30"}
	pat2 := &gitlab.PersonalAccessToken{ID: 200, Name: "token-agent-31"}
	file1 := writeFSToken(t, tc.Client.BaseURL().String(), 30, pat1)
	file2 := writeFSToken(t, tc.Client.BaseURL().String(), 31, pat2)

	out, err := exec("--filesystem --keyring=false --revoke=false --agent 30")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "Found 1 cached token(s) to clear")
	assert.Contains(t, out.String(), "Cleared token for agent 30 from filesystem")
	assert.NotContains(t, out.String(), "agent 31")

	// verify only agent 30 file deleted
	_, err = os.Stat(file1)
	assert.True(t, os.IsNotExist(err), "agent 30 token should be deleted")
	_, err = os.Stat(file2)
	assert.NoError(t, err, "agent 31 token should still exist")
}

func TestClear_KeyringRequiresAgentFlag(t *testing.T) {
	keyring.MockInit()
	tc := gitlab_testing.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.example.com"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("--filesystem=false --keyring=true --revoke=false")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "No cached tokens found to clear")
	assert.Contains(t, out.Stderr(), "Warning: failed to read keyring tokens")
	assert.Contains(t, out.Stderr(), "keyring token clearing requires --agent flag")
}
