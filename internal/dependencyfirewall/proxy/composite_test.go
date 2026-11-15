//go:build !integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

type ecoStub struct {
	eco     string
	matched bool
}

func (s ecoStub) Match(*http.Request) Match {
	if !s.matched {
		return Match{}
	}
	return Match{Matched: true, Pass: true, Coordinate: policy.Coordinate{Ecosystem: s.eco}}
}

func TestCompositeMatcherReturnsFirstMatch(t *testing.T) {
	t.Parallel()
	c := CompositeMatcher{ecoStub{"npm", false}, ecoStub{"pypi", true}, ecoStub{"gem", true}}
	got := c.Match(&http.Request{})
	require.True(t, got.Matched)
	assert.Equal(t, "pypi", got.Coordinate.Ecosystem)
}

func TestCompositeMatcherNoMatch(t *testing.T) {
	t.Parallel()
	c := CompositeMatcher{ecoStub{"npm", false}, ecoStub{"pypi", false}}
	assert.False(t, c.Match(&http.Request{}).Matched)
	assert.False(t, CompositeMatcher{}.Match(&http.Request{}).Matched)
}

func TestDefaultMatchersClaimEachEcosystem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		want policy.Coordinate
	}{
		{"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz", policy.Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}},
		{"https://files.pythonhosted.org/packages/ab/cd/requests-2.31.0-py3-none-any.whl", policy.Coordinate{Ecosystem: "pypi", Name: "requests", Version: "2.31.0"}},
		{"https://repo.maven.apache.org/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13.jar", policy.Coordinate{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "2.0.13"}},
		{"https://rubygems.org/gems/rails-7.1.3.gem", policy.Coordinate{Ecosystem: "gem", Name: "rails", Version: "7.1.3"}},
		{"https://crates.io/api/v1/crates/serde/1.0.197/download", policy.Coordinate{Ecosystem: "cargo", Name: "serde", Version: "1.0.197"}},
		{"https://api.nuget.org/v3-flatcontainer/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg", policy.Coordinate{Ecosystem: "nuget", Name: "newtonsoft.json", Version: "13.0.1"}},
	}
	for _, tt := range tests {
		t.Run(tt.want.Ecosystem, func(t *testing.T) {
			t.Parallel()
			got := DefaultMatchers().Match(httptest.NewRequest(http.MethodGet, tt.url, http.NoBody))
			require.True(t, got.Matched)
			assert.Equal(t, tt.want, got.Coordinate)
			assert.Equal(t, policy.Download, got.Operation)
		})
	}
}

func TestDefaultMatchersIgnoreNonJSONPut(t *testing.T) {
	t.Parallel()
	// A curl -T of an arbitrary file, or a cargo publish, must not be read as an
	// npm publish by the job-wide proxy.
	for _, contentType := range []string{"", "application/octet-stream"} {
		req := httptest.NewRequest(http.MethodPut, "https://artifacts.example.com/upload/build.tar.gz", strings.NewReader(`{"not":"a packument"}`))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		assert.False(t, DefaultMatchers().Match(req).Matched, "Content-Type %q", contentType)
	}
}

func TestDefaultMatchersInspectJSONPutAsNPMPublish(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(`{"name":"left-pad","versions":{"9.9.9":{}},"_attachments":{"left-pad-9.9.9.tgz":{}}}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	got := DefaultMatchers().Match(req)
	require.True(t, got.Matched)
	assert.Equal(t, "npm", got.Coordinate.Ecosystem)
	assert.Equal(t, policy.Upload, got.Operation)
}

func TestDefaultMatchersIgnoreGenericArchiveOffRegistryPath(t *testing.T) {
	t.Parallel()
	// A job-wide proxy sees unrelated toolchain downloads. A bare .tar.gz/.zip
	// off a non-registry path must not be claimed as a PyPI sdist, which would
	// evaluate a made-up coordinate and can fail the job closed on an API error.
	for _, url := range []string{
		"https://go.dev/dl/go1.22.3.linux-amd64.tar.gz",
		"https://nodejs.org/dist/v20.11.1/node-v20.11.1-linux-x64.tar.gz",
		"https://example.com/downloads/build-1.0.zip",
	} {
		assert.False(t, DefaultMatchers().Match(httptest.NewRequest(http.MethodGet, url, http.NoBody)).Matched, url)
	}
}

func TestDefaultMatchersClaimPyPIDownloads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, url, pkg, version string
	}{
		// A wheel is python-specific, so it is claimed on any path.
		{"wheel off a non-registry path", "https://pypi.example.com/simple/requests-2.31.0-py3-none-any.whl", "requests", "2.31.0"},
		// An sdist is claimed when the path carries the registry's /packages/ segment.
		{"sdist under /packages/", "https://files.pythonhosted.org/packages/ab/cd/requests-2.31.0.tar.gz", "requests", "2.31.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DefaultMatchers().Match(httptest.NewRequest(http.MethodGet, tt.url, http.NoBody))
			require.True(t, got.Matched)
			assert.Equal(t, policy.Coordinate{Ecosystem: "pypi", Name: tt.pkg, Version: tt.version}, got.Coordinate)
		})
	}
}
