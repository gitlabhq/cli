//go:build !integration

package policy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// restReq builds a Request with a project set, since the REST checker
// evaluates against Request.ProjectID.
func restReq(eco, name, version string) Request {
	r := req(eco, name, version)
	r.ProjectID = "group/proj"
	return r
}

// restCheckerWithMock returns a checker backed by the client-go mock and the
// mock recorder to program EvaluatePackage on.
func restCheckerWithMock(t *testing.T) (Checker, *gitlabtesting.TestClient) {
	t.Helper()
	tc := gitlabtesting.NewTestClient(t)
	return newRESTChecker(tc.Client), tc
}

func TestRESTAllowed(t *testing.T) {
	t.Parallel()
	c, tc := restCheckerWithMock(t)
	tc.MockSecurityDependencyFirewall.EXPECT().
		EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
		Return(&gitlab.PackageEvaluation{Outcome: gitlab.DependencyFirewallOutcomeAllowed}, nil, nil)

	res, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.NoError(t, err)
	assert.Equal(t, Result{}, res)
}

func TestRESTWarned(t *testing.T) {
	t.Parallel()
	c, tc := restCheckerWithMock(t)
	tc.MockSecurityDependencyFirewall.EXPECT().
		EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
		Return(&gitlab.PackageEvaluation{Outcome: gitlab.DependencyFirewallOutcomeWarned, Reason: new("deprecated")}, nil, nil)

	res, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Warning, res.Verdict)
	assert.Equal(t, "deprecated", res.Reason)
}

func TestRESTBlocked(t *testing.T) {
	t.Parallel()
	c, tc := restCheckerWithMock(t)
	tc.MockSecurityDependencyFirewall.EXPECT().
		EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
		Return(&gitlab.PackageEvaluation{Outcome: gitlab.DependencyFirewallOutcomeBlocked, Reason: new("malware")}, nil, nil)

	res, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Blocked, res.Verdict)
	assert.Equal(t, "malware", res.Reason)
}

func TestRESTUnknownOutcomeFailsClosed(t *testing.T) {
	t.Parallel()
	c, tc := restCheckerWithMock(t)
	tc.MockSecurityDependencyFirewall.EXPECT().
		EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
		Return(&gitlab.PackageEvaluation{Outcome: "maybe"}, nil, nil)

	_, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.Error(t, err)
}

func TestRESTNoEvaluationFailsClosed(t *testing.T) {
	t.Parallel()
	c, tc := restCheckerWithMock(t)
	tc.MockSecurityDependencyFirewall.EXPECT().
		EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
		Return(nil, nil, nil)

	_, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.Error(t, err)
}

func TestRESTUnsupportedEcosystemFailsClosed(t *testing.T) {
	t.Parallel()
	// No EvaluatePackage call is expected: an unsupported ecosystem is
	// rejected before the request is sent.
	c, _ := restCheckerWithMock(t)

	_, err := c.Check(t.Context(), restReq("cargo", "serde", "1.0.0"))
	require.Error(t, err)
}

func TestEcosystemValueMapsSupported(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ecosystem string
		want      gitlab.DependencyFirewallEcosystemValue
	}{
		{"npm", gitlab.DependencyFirewallEcosystemNPM},
		{"pypi", gitlab.DependencyFirewallEcosystemPyPI},
		{"maven", gitlab.DependencyFirewallEcosystemMaven},
		{"gem", gitlab.DependencyFirewallEcosystemGem},
	}
	for _, tt := range tests {
		t.Run(tt.ecosystem, func(t *testing.T) {
			t.Parallel()
			got, err := ecosystemValue(tt.ecosystem)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEcosystemValueRejectsUnsupported(t *testing.T) {
	t.Parallel()
	_, err := ecosystemValue("cargo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cargo")
}

// TestRESTStatusOutcomes covers how each API status maps to allow (via the
// errFirewallNotEvaluating sentinel) or a fail-closed error. gitlab.HasStatusCode
// reads the code from a *gitlab.ErrorResponse, so the mock returns one directly.
func TestRESTStatusOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		status       int
		wantSentinel bool
	}{
		{"404 not found fails open", http.StatusNotFound, true},
		{"422 not enforced fails open", http.StatusUnprocessableEntity, true},
		{"400 bad request fails closed", http.StatusBadRequest, false},
		{"401 unauthorized fails closed", http.StatusUnauthorized, false},
		{"403 forbidden fails closed", http.StatusForbidden, false},
		{"429 too many requests fails closed", http.StatusTooManyRequests, false},
		{"503 unavailable fails closed", http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, tc := restCheckerWithMock(t)
			tc.MockSecurityDependencyFirewall.EXPECT().
				EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
				Return(nil, nil, &gitlab.ErrorResponse{StatusCode: tt.status})

			res, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
			if tt.wantSentinel {
				require.ErrorIs(t, err, errFirewallNotEvaluating)
				assert.Equal(t, Result{}, res)
				return
			}
			require.Error(t, err)
			assert.NotErrorIs(t, err, errFirewallNotEvaluating)
		})
	}
}

func TestRESTTransportErrorFailsClosed(t *testing.T) {
	t.Parallel()
	c, tc := restCheckerWithMock(t)
	tc.MockSecurityDependencyFirewall.EXPECT().
		EvaluatePackage("group/proj", gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("dial tcp: connection refused"))

	_, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, errFirewallNotEvaluating)
}

// TestRESTSendsCorrectRequest is the one test that exercises the real HTTP
// wire format (method, path, JSON body) rather than the mock, to catch a
// serialization or path-escaping regression the mock cannot see.
func TestRESTSendsCorrectRequest(t *testing.T) {
	t.Parallel()
	type evaluateBody struct {
		Ecosystem string `json:"ecosystem"`
		Name      string `json:"name"`
		Version   string `json:"version"`
	}
	var gotMethod, gotPath string
	var gotBody evaluateBody
	var decodeErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		decodeErr = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"outcome":"allowed","reason":null}`)
	}))
	t.Cleanup(server.Close)
	client, err := gitlab.NewClient("", gitlab.WithBaseURL(server.URL+"/api/v4"))
	require.NoError(t, err)
	c := newRESTChecker(client)

	_, err = c.Check(t.Context(), restReq("maven", "org.slf4j:slf4j-api", "2.0.13"))
	require.NoError(t, err)
	require.NoError(t, decodeErr)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v4/projects/group%2Fproj/dependency_firewall/evaluate", gotPath)
	assert.Equal(t, evaluateBody{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "2.0.13"}, gotBody)
}
