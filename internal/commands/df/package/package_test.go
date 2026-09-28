//go:build !integration

package pkgcmd

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// runPackage drives the command against a real REST checker backed by client,
// so the test exercises the p.Type -> ecosystem mapping, the request shape,
// and the exit-code contract rather than the GLAB_DF_FAKE_* seam. It returns
// stdout and stderr separately so the allow-to-stdout / blocked-to-stderr
// contract can be asserted.
func runPackage(t *testing.T, client *gitlab.Client, purlArg string) (string, string, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	run := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("g", "p", "gitlab.com"),
		cmdtest.WithGitLabClient(client),
	)
	out, err := run(purlArg)
	if out == nil {
		return "", "", err
	}
	return out.OutBuf.String(), out.ErrBuf.String(), err
}

// mockClient returns a client whose EvaluatePackage is set up by setup. When
// setup is nil the API must not be called (the command should fail before the
// request, e.g. on an invalid PURL).
func mockClient(t *testing.T, setup func(tc *gitlabtesting.TestClient)) *gitlab.Client {
	t.Helper()
	tc := gitlabtesting.NewTestClient(t)
	if setup != nil {
		setup(tc)
	}
	return tc.Client
}

func TestPackageHelpTextHasNoFormatError(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	cmd := NewCmd(cmdtest.NewTestFactory(ios))
	assert.NotContains(t, cmd.Long, "%!", "Long help must not contain a fmt format-verb error")
	assert.NotContains(t, cmd.Long, "EXTRA", "Long help must not contain leftover fmt EXTRA args")
	assert.Contains(t, cmd.Long, "Exit codes:")
}

func TestPackageInvalidPURL(t *testing.T) {
	_, _, err := runPackage(t, mockClient(t, nil), "not-a-purl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid PURL")
}

func TestPackageUnsupportedType(t *testing.T) {
	_, _, err := runPackage(t, mockClient(t, nil), "pkg:cocoapods/Alamofire@5.9.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

// TestPackageVersionlessPURLRejected proves a PURL with no version fails
// locally with a clear error and never reaches the API, which would reject a
// blank version with 400. mockClient sets no EvaluatePackage expectation, so
// any call fails the test.
func TestPackageVersionlessPURLRejected(t *testing.T) {
	_, _, err := runPackage(t, mockClient(t, nil), "pkg:npm/left-pad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must include a version")
}

// requireExitCodeOne fails unless err is an *cmdutils.ExitError carrying the
// generic-failure exit code 1 (the contract for a misconfiguration or
// transport error) and returns it so the caller can assert its Details.
func requireExitCodeOne(t *testing.T, err error) *cmdutils.ExitError {
	t.Helper()
	require.Error(t, err)
	var xerr *cmdutils.ExitError
	require.ErrorAs(t, err, &xerr)
	assert.Equal(t, 1, xerr.Code)
	return xerr
}

// TestPackageBaseRepoErrorExitsOne covers the branch where the git remote
// cannot be resolved to a project: it must surface as exit 1, not a block.
func TestPackageBaseRepoErrorExitsOne(t *testing.T) {
	t.Chdir(t.TempDir())
	run := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBaseRepoError(errors.New("no GitLab remote found")))
	_, err := run("pkg:npm/left-pad@1.3.0")
	xerr := requireExitCodeOne(t, err)
	assert.Contains(t, xerr.Details, "failed to resolve GitLab project")
}

// TestPackageGitLabClientErrorExitsOne covers the branch where the API client
// cannot be built (e.g. missing or invalid token): it must surface as exit 1.
func TestPackageGitLabClientErrorExitsOne(t *testing.T) {
	t.Chdir(t.TempDir())
	run := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBaseRepo("g", "p", "gitlab.com"),
		cmdtest.WithGitLabClientError(errors.New("no token configured")))
	_, err := run("pkg:npm/left-pad@1.3.0")
	xerr := requireExitCodeOne(t, err)
	assert.Contains(t, xerr.Details, "failed to create a GitLab API client")
}

// TestPackageCheckerInitErrorExitsOne covers the branch where policy.New
// refuses to build a checker: under GITLAB_CI it rejects the GLAB_DF_FAKE_*
// fake so a pipeline variable cannot disable the firewall. That must surface
// as exit 1, not a block.
func TestPackageCheckerInitErrorExitsOne(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	_, _, err := runPackage(t, mockClient(t, nil), "pkg:npm/left-pad@1.3.0")
	xerr := requireExitCodeOne(t, err)
	assert.Contains(t, xerr.Details, "failed to initialize the Dependency Firewall policy checker")
}

// TestPackageSendsMappedRequest asserts the command forwards the resolved
// project, the PURL type mapped to the API ecosystem enum, and the download
// operation — the wiring the GLAB_DF_FAKE_* path never exercises.
func TestPackageSendsMappedRequest(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", &gitlab.EvaluatePackageOptions{
				Ecosystem: gitlab.DependencyFirewallEcosystemMaven,
				Name:      "org.slf4j:slf4j-api",
				Version:   "2.0.13",
			}, gomock.Any()).
			Return(&gitlab.PackageEvaluation{Outcome: gitlab.DependencyFirewallOutcomeAllowed}, nil, nil)
	})

	_, _, err := runPackage(t, client, "pkg:maven/org.slf4j/slf4j-api@2.0.13")
	require.NoError(t, err)
}

func TestPackageAllowReportsAllowed(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", gomock.Any(), gomock.Any()).
			Return(&gitlab.PackageEvaluation{Outcome: gitlab.DependencyFirewallOutcomeAllowed}, nil, nil)
	})

	stdout, _, err := runPackage(t, client, "pkg:npm/left-pad@1.3.0")
	require.NoError(t, err)
	assert.Contains(t, stdout, "left-pad@1.3.0")
	assert.Contains(t, stdout, "allowed by the Dependency Firewall")
	assert.NotContains(t, stdout, "no Dependency Firewall activity recorded",
		"an allow must not also print the empty-summary line")
}

// TestPackageAllowSanitizesCraftedPURL proves the direct allow-output path does
// not let a crafted PURL inject a newline or control sequence into the
// terminal: purl.Parse percent-decodes the version, and the command must
// flatten it before printing.
func TestPackageAllowSanitizesCraftedPURL(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", gomock.Any(), gomock.Any()).
			Return(&gitlab.PackageEvaluation{Outcome: gitlab.DependencyFirewallOutcomeAllowed}, nil, nil)
	})

	stdout, _, err := runPackage(t, client, "pkg:npm/left-pad@1.3.0%0a%0aFAKE-INJECTED-LINE")
	require.NoError(t, err)
	assert.NotContains(t, stdout, "\n\nFAKE-INJECTED-LINE",
		"a crafted version must not inject raw newlines into the output")
	assert.Contains(t, stdout, "left-pad@1.3.0 FAKE-INJECTED-LINE",
		"the crafted characters are flattened onto the single output line")
}

func TestPackageWarnExitsZero(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", gomock.Any(), gomock.Any()).
			Return(&gitlab.PackageEvaluation{
				Outcome: gitlab.DependencyFirewallOutcomeWarned,
				Reason:  new("known-malware policy"),
			}, nil, nil)
	})

	stdout, _, err := runPackage(t, client, "pkg:npm/left-pad@1.3.0")
	require.NoError(t, err)
	assert.Contains(t, stdout, "left-pad")
	assert.Contains(t, stdout, "1 package warning")
}

func TestPackageBlockedExitsThree(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", gomock.Any(), gomock.Any()).
			Return(&gitlab.PackageEvaluation{
				Outcome: gitlab.DependencyFirewallOutcomeBlocked,
				Reason:  new("blocked by policy"),
			}, nil, nil)
	})

	_, stderr, err := runPackage(t, client, "pkg:npm/left-pad@1.3.0")
	require.Error(t, err)

	var xerr *cmdutils.ExitError
	require.ErrorAs(t, err, &xerr, "expected *cmdutils.ExitError, got %T", err)
	assert.Equal(t, 3, xerr.Code)

	assert.Contains(t, stderr, "left-pad")
	assert.Contains(t, stderr, "1 package blocked")
}

// TestPackageNotEvaluatingFailsOpen covers a project the firewall is not
// evaluating (404): the command allows the package and exits 0 rather than
// treating the absence of a policy as a block.
func TestPackageNotEvaluatingFailsOpen(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", gomock.Any(), gomock.Any()).
			Return(nil, nil, &gitlab.ErrorResponse{StatusCode: http.StatusNotFound})
	})

	stdout, _, err := runPackage(t, client, "pkg:npm/left-pad@1.3.0")
	require.NoError(t, err)
	assert.Contains(t, stdout, "not evaluating this project")
}

// TestPackageTransportErrorExitsOne is the case CachingChecker used to erase:
// a transport failure must surface as the documented exit 1, not a false
// exit-3 block.
func TestPackageTransportErrorExitsOne(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("dial tcp: connection refused"))
	})

	_, _, err := runPackage(t, client, "pkg:npm/left-pad@1.3.0")
	require.Error(t, err)

	var xerr *cmdutils.ExitError
	require.ErrorAs(t, err, &xerr)
	assert.Equal(t, 1, xerr.Code, "a transport error is a misconfiguration/transport failure, not a block")
	assert.Contains(t, err.Error(), "connection refused")
}

// TestPackagePyPINormalizesNameForVerdict checks the PEP 503 normalization the
// command relies on: an input the parser must rewrite (Flask_Login ->
// flask-login) has to reach the API in normalized form for a policy keyed to
// the normalized name to match.
func TestPackagePyPINormalizesNameForVerdict(t *testing.T) {
	client := mockClient(t, func(tc *gitlabtesting.TestClient) {
		tc.MockSecurityDependencyFirewall.EXPECT().
			EvaluatePackage("g/p", &gitlab.EvaluatePackageOptions{
				Ecosystem: gitlab.DependencyFirewallEcosystemPyPI,
				Name:      "flask-login",
				Version:   "1.0",
			}, gomock.Any()).
			Return(&gitlab.PackageEvaluation{
				Outcome: gitlab.DependencyFirewallOutcomeBlocked,
				Reason:  new("blocked by policy"),
			}, nil, nil)
	})

	_, _, err := runPackage(t, client, "pkg:pypi/Flask_Login@1.0")
	require.Error(t, err)
	var xerr *cmdutils.ExitError
	require.ErrorAs(t, err, &xerr)
	assert.Equal(t, 3, xerr.Code)
}
