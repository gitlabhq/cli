//go:build !integration

package proxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func cargoReq(method, path string) *http.Request {
	return httptest.NewRequest(method, "https://crates.io"+path, http.NoBody)
}

func TestCargoMatchDownload(t *testing.T) {
	t.Parallel()
	m := CargoMatcher{}.Match(cargoReq(http.MethodGet, "/api/v1/crates/serde/1.0.197/download"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "cargo", Name: "serde", Version: "1.0.197"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestCargoMatchSparseDownloadEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		host    string
		path    string
		crate   string
		version string
	}{
		{
			// crates.io's config.json "dl" has no markers, so cargo appends
			// "/{crate}/{version}/download" to the "static.crates.io/crates"
			// base — no "/api/v1" prefix. This is the real request cargo 1.96
			// makes and is what regressed as unmatched.
			name:    "crates.io sparse download endpoint",
			host:    "https://static.crates.io",
			path:    "/crates/hashbrown/0.17.1/download",
			crate:   "hashbrown",
			version: "0.17.1",
		},
		{
			name:    "hyphenated crate on download endpoint",
			host:    "https://static.crates.io",
			path:    "/crates/allocator-api2/0.2.21/download",
			crate:   "allocator-api2",
			version: "0.2.21",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.host+tc.path, http.NoBody)
			m := CargoMatcher{}.Match(req)
			assert.True(t, m.Matched)
			assert.True(t, m.Pass)
			assert.Equal(t, policy.Coordinate{Ecosystem: "cargo", Name: tc.crate, Version: tc.version}, m.Coordinate)
			assert.Equal(t, policy.Download, m.Operation)
		})
	}
}

func TestCargoMatchSparseDownload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		host    string
		path    string
		crate   string
		version string
	}{
		{
			name:    "crates.io sparse cdn",
			host:    "https://static.crates.io",
			path:    "/crates/anyhow/anyhow-1.0.104.crate",
			crate:   "anyhow",
			version: "1.0.104",
		},
		{
			name:    "hyphenated crate name",
			host:    "https://static.crates.io",
			path:    "/crates/serde-derive/serde-derive-1.0.197.crate",
			crate:   "serde-derive",
			version: "1.0.197",
		},
		{
			name:    "prerelease version",
			host:    "https://static.crates.io",
			path:    "/crates/foo/foo-1.0.0-beta.1.crate",
			crate:   "foo",
			version: "1.0.0-beta.1",
		},
		{
			name:    "third-party registry path layout",
			host:    "https://artifactory.example.com",
			path:    "/artifactory/api/cargo/crates-remote/serde-1.0.197.crate",
			crate:   "serde",
			version: "1.0.197",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.host+tc.path, http.NoBody)
			m := CargoMatcher{}.Match(req)
			assert.True(t, m.Matched)
			assert.True(t, m.Pass)
			assert.Equal(t, policy.Coordinate{Ecosystem: "cargo", Name: tc.crate, Version: tc.version}, m.Coordinate)
			assert.Equal(t, policy.Download, m.Operation)
		})
	}
}

func TestCargoMetadataIsNoMatch(t *testing.T) {
	t.Parallel()
	m := CargoMatcher{}.Match(cargoReq(http.MethodGet, "/api/v1/crates/serde"))
	assert.False(t, m.Matched)
}

func TestCargoSparseUnparseableCrateFailsClosed(t *testing.T) {
	t.Parallel()
	// A ".crate" filename is recognizably a crate download; one this heuristic
	// cannot split into name/version must fail closed (Matched, !Pass) rather
	// than falling out of classification and being forwarded uninspected.
	cases := []string{
		"https://static.crates.io/crates/foo/foo.crate",
		"https://static.crates.io/crates/foo/1.0.0.crate",
		"https://static.crates.io/crates/foo/.crate",
	}
	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			m := CargoMatcher{}.Match(req)
			assert.True(t, m.Matched)
			assert.False(t, m.Pass, "an unparseable crate download must fail closed (Pass = false)")
			assert.Equal(t, policy.Download, m.Operation)
			assert.Equal(t, "cargo", m.Coordinate.Ecosystem)
		})
	}
}

func cargoPublishBody(name, vers string) []byte {
	jsonMeta := []byte(`{"name":"` + name + `","vers":"` + vers + `"}`)
	crate := []byte("fake-crate-bytes")
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(jsonMeta)))
	b.Write(jsonMeta)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(crate)))
	b.Write(crate)
	return b.Bytes()
}

func TestCargoMatchPublish(t *testing.T) {
	t.Parallel()
	body := cargoPublishBody("mycrate", "0.2.0")
	req := httptest.NewRequest(http.MethodPut, "https://crates.io/api/v1/crates/new", bytes.NewReader(body))

	m := CargoMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Upload, m.Operation)
	assert.Equal(t, "mycrate", m.Coordinate.Name)
	assert.Equal(t, "0.2.0", m.Coordinate.Version)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, body, restored)
}

func TestCargoPublishMalformedFrameFailsClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body []byte
	}{
		{"too short", []byte{0x01, 0x02}},
		{"zero json len", func() []byte {
			var b bytes.Buffer
			_ = binary.Write(&b, binary.LittleEndian, uint32(0))
			return b.Bytes()
		}()},
		{"json len exceeds body", func() []byte {
			var b bytes.Buffer
			_ = binary.Write(&b, binary.LittleEndian, uint32(9999))
			b.WriteString("{}")
			return b.Bytes()
		}()},
		{"json len high bit set", func() []byte {
			var b bytes.Buffer
			_ = binary.Write(&b, binary.LittleEndian, uint32(0x80000000))
			b.WriteString(`{"name":"x","vers":"1"}`)
			return b.Bytes()
		}()},
		{"bad json", func() []byte {
			var b bytes.Buffer
			meta := []byte("not json")
			_ = binary.Write(&b, binary.LittleEndian, uint32(len(meta)))
			b.Write(meta)
			return b.Bytes()
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPut, "https://crates.io/api/v1/crates/new", bytes.NewReader(tc.body))
			m := CargoMatcher{}.Match(req)
			// The request matched "PUT …/api/v1/crates/new" exactly, so a frame
			// we cannot decode fails closed rather than falling out of scope.
			assert.True(t, m.Matched)
			assert.False(t, m.Pass, "an unparseable publish frame must fail closed (Pass = false)")
			assert.Equal(t, policy.Upload, m.Operation)
			assert.Equal(t, "cargo", m.Coordinate.Ecosystem)
		})
	}
}

func TestCargoPublishOverLimitFailsClosed(t *testing.T) {
	t.Parallel()
	body := cargoPublishBody("mycrate", "0.2.0")
	req := httptest.NewRequest(http.MethodPut, "https://crates.io/api/v1/crates/new", bytes.NewReader(body))
	m := CargoMatcher{limits: peekLimits{inMemory: 4, max: 8}}.Match(req)
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "an over-limit upload must fail closed (Pass = false)")
	assert.Equal(t, policy.Upload, m.Operation)
}
