//go:build !integration

package proxy

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func TestBlockResponseStatusLineCarriesReason(t *testing.T) {
	t.Parallel()
	resp := parseBlock(t, blockResponse("maven", "Package 'g/a' violates 'Deny log4j' policy", false))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "403 Blocked by GitLab Dependency Firewall: Package 'g/a' violates 'Deny log4j' policy", resp.Status)
}

func TestBlockResponseStatusLineWithoutReason(t *testing.T) {
	t.Parallel()
	resp := parseBlock(t, blockResponse("maven", "", false))
	defer resp.Body.Close()
	assert.Equal(t, "403 Blocked by GitLab Dependency Firewall", resp.Status)
}

func TestBlockResponseStatusLineCannotInjectHeaders(t *testing.T) {
	t.Parallel()
	resp := parseBlock(t, blockResponse("maven", "nope\r\nX-Injected: yes\r\n\r\nbody", false))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("X-Injected"))
	assert.Equal(t, "403 Blocked by GitLab Dependency Firewall: nope X-Injected: yes body", resp.Status)
}

func TestBlockResponseStatusLineHidesFailedCheckError(t *testing.T) {
	t.Parallel()
	resp := parseBlock(t, blockResponse("maven", "policy check failed: GET https://gitlab.example/api/v4/projects/9/x: 500 {host=10.0.0.5}", true))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "403 Dependency Firewall could not evaluate this package", resp.Status)
}

func TestBlockResponseStatusLineIsCapped(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 500)
	resp := parseBlock(t, blockResponse("gem", long, false))
	defer resp.Body.Close()
	assert.LessOrEqual(t, len(resp.Status), len("403 ")+512)
	assert.True(t, utf8.ValidString(resp.Status), "the cap must not split a rune")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, long, string(body), "the body keeps the full reason")
}

func parseBlock(t *testing.T, raw string) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	require.NoError(t, err)
	return resp
}

func TestBlockResponseNPM(t *testing.T) {
	t.Parallel()
	raw := blockResponse("npm", "blocked by policy", false)
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "blocked", resp.Header.Get("X-Gitlab-Dependency-Firewall"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "blocked by policy")
}

func TestBlockResponsePyPIIsHTML(t *testing.T) {
	t.Parallel()
	raw := blockResponse("pypi", "nope", false)
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestBlockResponseGemIsPlainText(t *testing.T) {
	t.Parallel()
	raw := blockResponse("gem", "nope", false)
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"))
}

func TestBlockResponseMavenIsHTML(t *testing.T) {
	t.Parallel()
	raw := blockResponse("maven", "nope", false)
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
}

func TestBlockResponseNuGetIsJSON(t *testing.T) {
	t.Parallel()
	raw := blockResponse("nuget", "blocked by policy", false)
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "blocked by policy")
}

func TestInternalErrorStatusLineCannotInjectHeaders(t *testing.T) {
	t.Parallel()
	phrase := policy.InternalErrorPhrase(policy.Coordinate{Name: "org.evil\r\nX-Injected: yes\r\n\r\nbody:a", Version: "1"})
	resp := parseBlock(t, blockResponse("maven", phrase, true))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("X-Injected"))

	long := parseBlock(t, blockResponse("maven", policy.InternalErrorPhrase(policy.Coordinate{Name: strings.Repeat("a", 2000), Version: "1"}), true))
	defer long.Body.Close()
	assert.LessOrEqual(t, len(long.Status), len("403 ")+maxBlockReasonPhrase)
}
