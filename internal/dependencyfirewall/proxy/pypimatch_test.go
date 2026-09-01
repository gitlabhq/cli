//go:build !integration

package proxy

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func TestPyPIMatchWheelDownload(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet,
		"https://files.pythonhosted.org/packages/ab/cd/requests-2.31.0-py3-none-any.whl", nil)
	m := PyPIMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "pypi", Name: "requests", Version: "2.31.0"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestPyPIMatchWheelMetadataDownload(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet,
		"https://files.pythonhosted.org/packages/ab/cd/requests-2.31.0-py3-none-any.whl.metadata", nil)
	m := PyPIMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "pypi", Name: "requests", Version: "2.31.0"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestPyPIMatchSdistDownload(t *testing.T) {
	t.Parallel()
	// PyPI serves sdists as .tar.gz or .zip (modern) and still serves the
	// legacy .tar.bz2 and .tgz for older packages. Every served format must be
	// policy-checked; an unrecognized one would be forwarded without a check.
	for _, ext := range []string{".tar.gz", ".zip", ".tar.bz2", ".tgz"} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet,
				"https://files.pythonhosted.org/packages/ab/cd/Flask-3.0.2"+ext, nil)
			m := PyPIMatcher{}.Match(req)
			assert.True(t, m.Matched)
			assert.True(t, m.Pass)
			assert.Equal(t, "flask", m.Coordinate.Name)
			assert.Equal(t, "3.0.2", m.Coordinate.Version)
		})
	}
}

func TestPyPISimpleIndexIsNoMatch(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "https://pypi.org/simple/requests/", nil)
	m := PyPIMatcher{}.Match(req)
	assert.False(t, m.Matched)
}

func TestPyPIMatchUpload(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("name", "requests"))
	require.NoError(t, w.WriteField("version", "2.31.0"))
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "https://upload.pypi.org/legacy/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	m := PyPIMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "requests", m.Coordinate.Name)
	assert.Equal(t, "2.31.0", m.Coordinate.Version)
	assert.Equal(t, policy.Upload, m.Operation)
}

func TestPyPIMatchUploadIgnoresNonLegacyPath(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("name", "requests"))
	require.NoError(t, w.WriteField("version", "2.31.0"))
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "https://example.com/some/other/endpoint", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	m := PyPIMatcher{}.Match(req)
	assert.False(t, m.Matched, "a multipart POST outside the /legacy/ upload endpoint must not be treated as a PyPI upload")
}

func TestPyPIMatchUploadOverLimitFailsClosed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("name", "requests"))
	require.NoError(t, w.WriteField("version", "2.31.0"))
	require.NoError(t, w.Close())
	// Ensure the body clears the 64-byte hard limit set above.
	require.Greater(t, buf.Len(), 64)

	req := httptest.NewRequest(http.MethodPost, "https://upload.pypi.org/legacy/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	m := PyPIMatcher{limits: peekLimits{inMemory: 16, max: 64}}.Match(req)
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "an over-limit upload must fail closed (Pass = false)")
	assert.Equal(t, "pypi", m.Coordinate.Ecosystem)
	assert.Equal(t, policy.Upload, m.Operation)
}
