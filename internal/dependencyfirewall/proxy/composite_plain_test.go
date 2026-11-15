//go:build !integration

package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// startJobProxy starts a proxy configured like the job-level one and returns
// a client that sends plain HTTP through it, as a job's tools do once
// HTTP_PROXY points at it.
func startJobProxy(t *testing.T, checker policy.Checker) (*Proxy, *http.Client, *atomic.Int32) {
	t.Helper()
	var hooked atomic.Int32
	p, err := New(DefaultMatchers(), checker, "group/proj",
		WithVerdictHook(func(verdict.Entry) { hooked.Add(1) }))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)
	proxyURL, err := url.Parse("http://" + p.Addr())
	require.NoError(t, err)
	return p, &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}, &hooked
}

func TestJobProxyBlocksPlainHTTPPackageDownloads(t *testing.T) {
	t.Parallel()
	// RubyGems and PyPI tools follow only the generic proxy variables, so
	// without HTTP_PROXY a plain-HTTP registry for them would go uninspected.
	for _, path := range []string{
		"/left-pad/-/left-pad-1.3.0.tgz",
		"/gems/rails-7.1.3.gem",
		"/packages/ab/cd/requests-2.31.0-py3-none-any.whl",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			t.Cleanup(upstream.Close)
			p, client, hooked := startJobProxy(t, stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "malicious"}})

			resp, err := client.Get(upstream.URL + path)
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
			assert.Zero(t, hits.Load(), "a blocked download must not reach the registry")
			require.Len(t, p.Verdicts(), 1)
			assert.Equal(t, verdict.Blocked, p.Verdicts()[0].Verdict)
			assert.Equal(t, int32(1), hooked.Load(), "the verdict reaches the CI log hook before the 403")
		})
	}
}

func TestJobProxyForwardsOtherPlainHTTPTraffic(t *testing.T) {
	t.Parallel()
	// Such as a job calling a service container: not a package, so no policy
	// check and no verdict, just forwarded.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok "+r.Method+" "+r.URL.Path)
	}))
	t.Cleanup(upstream.Close)
	p, client, hooked := startJobProxy(t, stubChecker{result: policy.Result{Verdict: verdict.Blocked}})

	resp, err := client.Get(upstream.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok GET /health", string(body))
	assert.Empty(t, p.Verdicts())
	assert.Zero(t, hooked.Load())
}
