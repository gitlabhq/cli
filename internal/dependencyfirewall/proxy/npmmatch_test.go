//go:build !integration

package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func get(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "https://registry.npmjs.org"+path, nil)
}

func TestNPMMatchTarballDownload(t *testing.T) {
	t.Parallel()
	m := NPMMatcher{}.Match(get("/left-pad/-/left-pad-1.3.0.tgz"))
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestNPMMatchScopedTarball(t *testing.T) {
	t.Parallel()
	m := NPMMatcher{}.Match(get("/@babel/core/-/core-7.24.0.tgz"))
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "@babel/core", m.Coordinate.Name)
	assert.Equal(t, "7.24.0", m.Coordinate.Version)
}

func TestNPMMatchMetadataIsNoMatch(t *testing.T) {
	t.Parallel()
	m := NPMMatcher{}.Match(get("/left-pad"))
	assert.False(t, m.Matched)
}

func TestNPMMatchUploadPut(t *testing.T) {
	t.Parallel()
	body := `{"name":"left-pad","versions":{"9.9.9":{}}}`
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))
	m := NPMMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "left-pad", m.Coordinate.Name)
	assert.Equal(t, "9.9.9", m.Coordinate.Version)
	assert.Equal(t, policy.Upload, m.Operation)

	rest, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(rest))
}

func TestNPMMatchUploadSpillsButStillMatches(t *testing.T) {
	t.Parallel()
	// The packument JSON sits at the front and fits within the in-memory
	// window, but trailing attachment bytes push the total body past it so
	// it spills to a temp file. The coordinate must still be extracted and
	// the full body restored for the upstream round trip.
	json := `{"name":"left-pad","versions":{"9.9.9":{}}}`
	body := json + strings.Repeat(" ", 300) // total > in-memory window below
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))

	m := NPMMatcher{limits: peekLimits{inMemory: int64(len(json) + 8), max: 1 << 20}}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "left-pad", m.Coordinate.Name)
	assert.Equal(t, "9.9.9", m.Coordinate.Version)

	rest, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())
	assert.Equal(t, body, string(rest), "full body must reach upstream even when spilled")
}

func TestNPMMatchUploadOverLimitFailsClosed(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 200) // exceeds the 64-byte hard limit
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))

	m := NPMMatcher{limits: peekLimits{inMemory: 16, max: 64}}.Match(req)
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "an over-limit upload must fail closed (Pass = false)")
	assert.Equal(t, "npm", m.Coordinate.Ecosystem)
	assert.Equal(t, policy.Upload, m.Operation)
}
