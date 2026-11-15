//go:build !integration

package proxy

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

func TestVerdictHookRunsOncePerVerdictBeforeTheBlockedResponse(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()

	var (
		mu    sync.Mutex
		hooks []verdict.Entry
		p     *Proxy
	)
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	p, err := New(stubMatcher{}, stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "nope"}}, "group/proj",
		WithUpstreamRootCAs(pool),
		WithVerdictHook(func(e verdict.Entry) {
			// Verdicts takes the proxy's lock, so this deadlocks if the hook is
			// called while record still holds it.
			_ = p.Verdicts()
			mu.Lock()
			defer mu.Unlock()
			hooks = append(hooks, e)
		}),
	)
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)
	client := newClient(t, p)

	for range 2 {
		resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)

		// The client has its 403, so the hook must already have run.
		mu.Lock()
		got := len(hooks)
		mu.Unlock()
		assert.Equal(t, 1, got, "hook runs before the blocked response, and once for a repeated verdict")
	}
	assert.Equal(t, verdict.Blocked, hooks[0].Verdict)
}
