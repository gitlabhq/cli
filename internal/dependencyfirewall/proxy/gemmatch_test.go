//go:build !integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func rubygem(method, path string) *http.Request {
	return httptest.NewRequest(method, "https://rubygems.org"+path, nil)
}

func TestGemMatchDownload(t *testing.T) {
	t.Parallel()
	m := GemMatcher{}.Match(rubygem(http.MethodGet, "/gems/rails-7.1.3.gem"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "gem", Name: "rails", Version: "7.1.3"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestGemMatchPlatformSuffix(t *testing.T) {
	t.Parallel()
	m := GemMatcher{}.Match(rubygem(http.MethodGet, "/gems/nokogiri-1.16.0-x86_64-linux.gem"))
	assert.True(t, m.Matched)
	assert.Equal(t, "nokogiri", m.Coordinate.Name)
	assert.Equal(t, "1.16.0", m.Coordinate.Version)
}

func TestGemInfoEndpointIsNoMatch(t *testing.T) {
	t.Parallel()
	m := GemMatcher{}.Match(rubygem(http.MethodGet, "/info/rails"))
	assert.False(t, m.Matched)
}

func TestGemUploadIsMatchWithoutVersion(t *testing.T) {
	t.Parallel()
	m := GemMatcher{}.Match(rubygem(http.MethodPost, "/api/v1/gems"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Upload, m.Operation)
	assert.Empty(t, m.Coordinate.Name)
}

func TestGemDownloadRequiresGemsAsImmediateParent(t *testing.T) {
	t.Parallel()
	// "gems" must be the .gem file's immediate parent directory. A path where
	// "gems" is only an ancestor (here, a "subdir" sits between) is not a
	// genuine RubyGems download coordinate and must not match, otherwise
	// unrelated ".gem" traffic under any "gems/" ancestor bypasses the check.
	m := GemMatcher{}.Match(rubygem(http.MethodGet, "/vendor/gems/subdir/rails-7.1.3.gem"))
	assert.False(t, m.Matched, "gems must be the immediate parent directory of the .gem file")
}

func TestGemDownloadAllowsMountPrefix(t *testing.T) {
	t.Parallel()
	// An arbitrary mount prefix before "gems/" is still a valid download as
	// long as "gems" is the immediate parent of the .gem file.
	m := GemMatcher{}.Match(rubygem(http.MethodGet, "/mirror/rubygems/gems/rails-7.1.3.gem"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "gem", Name: "rails", Version: "7.1.3"}, m.Coordinate)
}

func TestGemDoesNotDoubleDecodePath(t *testing.T) {
	t.Parallel()
	// req.URL.Path is already decoded once by Go's parser. The matcher must
	// evaluate that once-decoded path and not decode it a second time. Simulate
	// a parser output where the file segment still contains a literal "%2e":
	// upstream sees "...rails-7.1.3%2egem" (not a ".gem"), so it must not match.
	// A second PathUnescape would turn "%2egem" into ".gem" and wrongly match.
	req := rubygem(http.MethodGet, "/gems/rails-7.1.3.gem")
	req.URL.Path = "/gems/rails-7.1.3%2egem"
	m := GemMatcher{}.Match(req)
	assert.False(t, m.Matched, "matcher must not perform a second percent-decode of req.URL.Path")
}

func TestGemUploadPathBoundary(t *testing.T) {
	t.Parallel()
	// The upload match must anchor on a path-segment boundary: a path that
	// merely ends in "api/v1/gems" without a preceding "/" (or being the whole
	// path) is not the upload endpoint and must not match.
	m := GemMatcher{}.Match(rubygem(http.MethodPost, "/notapi/v1/gems"))
	assert.False(t, m.Matched)
}
