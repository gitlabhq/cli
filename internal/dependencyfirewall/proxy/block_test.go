//go:build !integration

package proxy

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseBlock(t *testing.T, raw string) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	require.NoError(t, err)
	return resp
}

func TestBlockResponseNPM(t *testing.T) {
	t.Parallel()
	raw := blockResponse("npm", "blocked by policy")
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
	raw := blockResponse("pypi", "nope")
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestBlockResponseGemIsPlainText(t *testing.T) {
	t.Parallel()
	raw := blockResponse("gem", "nope")
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"))
}

func TestBlockResponseMavenIsHTML(t *testing.T) {
	t.Parallel()
	raw := blockResponse("maven", "nope")
	resp := parseBlock(t, raw)
	defer resp.Body.Close()
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
}
