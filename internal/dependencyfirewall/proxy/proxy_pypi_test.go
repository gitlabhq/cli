//go:build !integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// TestProxyPyPIMetadataBlockIsRecorded: a blocked package's only request is
// pip's PEP 658 .whl.metadata sidecar, which must still record a verdict. It
// exercises the proxy end-to-end through the real PyPIMatcher, so it lives
// with the ecosystem matchers rather than in proxy_test.go.
func TestProxyPyPIMetadataBlockIsRecorded(t *testing.T) {
	t.Parallel()
	upstreamHit := false
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	p := startProxyWithMatcher(t, upstream, PyPIMatcher{},
		stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "high-severity vulnerability"}})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/packages/be/9e/lxml-4.9.4-cp312-cp312-manylinux_2_28_x86_64.whl.metadata")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, upstreamHit, "upstream must not be contacted on block")

	verdicts := p.Verdicts()
	require.Len(t, verdicts, 1)
	t.Logf("recorded verdict (written to .gitlab/df/ci-log.json): %+v", verdicts[0])
	assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	assert.Equal(t, "lxml", verdicts[0].Package)
	assert.Equal(t, "4.9.4", verdicts[0].Version)
}
