//go:build !integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type respOpts struct {
	status        int
	contentType   string
	path          string
	contentLength int64
	uncompressed  bool
}

func streamResp(o respOpts) *http.Response {
	return &http.Response{
		StatusCode:    o.status,
		Header:        http.Header{"Content-Type": {o.contentType}},
		ContentLength: o.contentLength,
		Uncompressed:  o.uncompressed,
		Body:          http.NoBody,
		Request:       httptest.NewRequest(http.MethodGet, "https://example.com"+o.path, http.NoBody),
	}
}

func TestIsStreamable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts respOpts
		want bool
	}{
		{
			name: "large botocore wheel binary/octet-stream streams",
			opts: respOpts{200, "binary/octet-stream", "/botocore-1.43.89-py3-none-any.whl", 15768272, false},
			want: true,
		},
		{
			name: "crate application/gzip streams",
			opts: respOpts{200, "application/gzip", "/crates/anyhow/anyhow-1.0.104.crate", 48819, false},
			want: true,
		},
		{
			name: "wheel by extension with text content-type streams",
			opts: respOpts{200, "text/plain", "/foo-1.0-py3-none-any.whl", 1234, false},
			want: true,
		},
		{
			name: "application octet-stream with length streams",
			opts: respOpts{200, "application/octet-stream", "/x", 1000, false},
			want: true,
		},
		{
			name: "tarball .tar.gz by extension streams",
			opts: respOpts{200, "text/plain", "/pkg-1.0.tar.gz", 2048, false},
			want: true,
		},
		{
			name: "tarball .tar.bz2 by extension streams",
			opts: respOpts{200, "text/plain", "/pkg-1.0.tar.bz2", 2048, false},
			want: true,
		},
		{
			name: "gem by extension streams",
			opts: respOpts{200, "text/plain", "/gems/rake-13.2.1.gem", 4096, false},
			want: true,
		},
		{
			name: "jar by extension streams",
			opts: respOpts{200, "text/plain", "/com/example/foo/1.0/foo-1.0.jar", 8192, false},
			want: true,
		},
		{
			name: "nupkg by extension streams",
			opts: respOpts{200, "text/plain", "/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg", 16384, false},
			want: true,
		},
		{
			name: "war by extension streams",
			opts: respOpts{200, "text/plain", "/com/example/app/1.0/app-1.0.war", 29745860, false},
			want: true,
		},
		{
			name: "aar by extension streams",
			opts: respOpts{200, "text/plain", "/com/example/lib/1.0/lib-1.0.aar", 2293265, false},
			want: true,
		},
		{
			name: "maven central war served application/java-archive streams",
			opts: respOpts{200, "application/java-archive", "/org/apache/solr/solr/4.10.4/solr-4.10.4.war", 29745860, false},
			want: true,
		},
		{
			name: "application/java-archive content-type streams",
			opts: respOpts{200, "application/java-archive", "/download", 1631109, false},
			want: true,
		},
		{
			name: "application/x-gzip content-type streams",
			opts: respOpts{200, "application/x-gzip", "/download", 3072, false},
			want: true,
		},
		{
			name: "application/zip content-type streams",
			opts: respOpts{200, "application/zip", "/download", 5120, false},
			want: true,
		},
		{
			name: "metadata sidecar unknown length is buffered (regression)",
			opts: respOpts{200, "binary/octet-stream", "/boto3-1.43.89-py3-none-any.whl.metadata", -1, true},
			want: false,
		},
		{
			name: "artifact with unknown content length is buffered",
			opts: respOpts{200, "binary/octet-stream", "/foo.whl", -1, false},
			want: false,
		},
		{
			name: "transport-decompressed artifact is buffered",
			opts: respOpts{200, "application/octet-stream", "/foo.whl", 500, true},
			want: false,
		},
		{
			name: "json metadata is buffered",
			opts: respOpts{200, "application/json", "/simple/boto3/", 200, false},
			want: false,
		},
		{
			name: "html metadata is buffered",
			opts: respOpts{200, "text/html", "/simple/boto3/", 200, false},
			want: false,
		},
		{
			name: "non-2xx is not streamed",
			opts: respOpts{302, "application/octet-stream", "/foo.whl", 100, false},
			want: false,
		},
		{
			name: "404 is not streamed",
			opts: respOpts{404, "application/octet-stream", "/foo.whl", 100, false},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := streamResp(tc.opts)
			defer resp.Body.Close()
			assert.Equal(t, tc.want, isStreamable(resp))
		})
	}
}
