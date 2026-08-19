//go:build !integration

package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

func fakeFrom(env map[string]string) Checker {
	var environ []string
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	return newFakeChecker(environ)
}

func req(eco, name, version string) Request {
	return Request{Coordinate: Coordinate{Ecosystem: eco, Name: name, Version: version}}
}

func TestFakeDefaultAllow(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_DEFAULT": "allow"})
	r, err := c.Check(t.Context(), req("npm", "left-pad", "1.3.0"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict(""), r.Verdict)
}

func TestFakeDefaultEmptyIsAllow(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_WARN": ""})
	r, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict(""), r.Verdict)
}

func TestFakeDefaultUnrecognizedIsAllow(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_DEFAULT": "blck"})
	r, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict(""), r.Verdict)
}

func TestFakeBlockExactVersion(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_BLOCK": "npm:left-pad@1.3.0"})
	r, _ := c.Check(t.Context(), req("npm", "left-pad", "1.3.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)

	r2, _ := c.Check(t.Context(), req("npm", "left-pad", "2.0.0"))
	assert.Equal(t, verdict.Verdict(""), r2.Verdict)
}

func TestFakeBlockScopedNPMExactVersion(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_BLOCK": "npm:@babel/core@7.24.0"})
	r, _ := c.Check(t.Context(), req("npm", "@babel/core", "7.24.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)

	r2, _ := c.Check(t.Context(), req("npm", "@babel/core", "8.0.0"))
	assert.Equal(t, verdict.Verdict(""), r2.Verdict)
}

func TestFakeBlockScopedNPMAnyVersion(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_BLOCK": "npm:@babel/core"})
	r, _ := c.Check(t.Context(), req("npm", "@babel/core", "7.24.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)
}

func TestFakeBlockAnyVersion(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_BLOCK": "pypi:requests"})
	r, _ := c.Check(t.Context(), req("pypi", "requests", "2.31.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)
}

func TestFakeWarn(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_WARN": "gem:rails@7.1.3"})
	r, _ := c.Check(t.Context(), req("gem", "rails", "7.1.3"))
	assert.Equal(t, verdict.Warning, r.Verdict)
}

func TestFakeExactBeatsWildcard(t *testing.T) {
	c := fakeFrom(map[string]string{
		"GLAB_DF_FAKE_WARN":  "npm:left-pad",
		"GLAB_DF_FAKE_BLOCK": "npm:left-pad@1.3.0",
	})
	r, _ := c.Check(t.Context(), req("npm", "left-pad", "1.3.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)
}

func TestFakeBlockBeforeWarnSameSpecificity(t *testing.T) {
	c := fakeFrom(map[string]string{
		"GLAB_DF_FAKE_BLOCK": "npm:pkg",
		"GLAB_DF_FAKE_WARN":  "npm:pkg",
	})
	r, _ := c.Check(t.Context(), req("npm", "pkg", "1.0.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)
}

func TestFakeDefaultBlock(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_DEFAULT": "block"})
	r, _ := c.Check(t.Context(), req("npm", "anything", "1.0.0"))
	assert.Equal(t, verdict.Blocked, r.Verdict)
}

func TestFakeDefaultWarn(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_DEFAULT": "warn"})
	r, _ := c.Check(t.Context(), req("npm", "anything", "1.0.0"))
	assert.Equal(t, verdict.Warning, r.Verdict)
}

func TestFakeParseListSkipsMalformedEntries(t *testing.T) {
	c := fakeFrom(map[string]string{"GLAB_DF_FAKE_BLOCK": "npm:, ,:x,pypi:requests"})
	blocked, _ := c.Check(t.Context(), req("pypi", "requests", "2.31.0"))
	assert.Equal(t, verdict.Blocked, blocked.Verdict)

	allowed, _ := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	assert.Equal(t, verdict.Verdict(""), allowed.Verdict)
}
