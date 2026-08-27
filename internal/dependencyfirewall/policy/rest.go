package policy

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/purl"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// errFirewallNotEvaluating reports that the Dependency Firewall is not
// evaluating this project, so there is no verdict to apply. The API signals
// this with 404 (feature flag off, project not found, or token without access)
// or 422 (firewall configured but not enforced). CachingChecker maps it to
// allow; restChecker never decides the fail-open policy itself.
var errFirewallNotEvaluating = errors.New("dependency firewall is not evaluating this project")

// restChecker is the real GitLab-backed policy source, selected whenever no
// GLAB_DF_FAKE_* variable is set. It evaluates each coordinate against the
// firewall for the request's project and maps the outcome to a Result. It
// returns errFirewallNotEvaluating when the firewall is not evaluating the
// project (so the caller can allow), and a wrapped transport/server/
// unexpected-response error on any other failure (so the caller can block).
type restChecker struct {
	client *gitlab.Client
}

func newRESTChecker(client *gitlab.Client) Checker {
	return restChecker{client: client}
}

func (c restChecker) Check(ctx context.Context, req Request) (Result, error) {
	ecosystem, err := ecosystemValue(req.Coordinate.Ecosystem)
	if err != nil {
		return Result{}, err
	}

	opt := &gitlab.EvaluatePackageOptions{
		Ecosystem: ecosystem,
		Name:      req.Coordinate.Name,
		Version:   req.Coordinate.Version,
	}

	eval, _, err := c.client.SecurityDependencyFirewall.EvaluatePackage(req.ProjectID, opt, gitlab.WithContext(ctx))

	// The firewall is deliberately not evaluating this project on 404 (feature
	// flag off / project not found / no access) or 422 (not enforced). Signal
	// that with a sentinel so the caller, not this checker, owns the fail-open
	// decision. EvaluatePackage carries the status on the returned error, read
	// via gitlab.HasStatusCode. Wrap the underlying error so the sentinel keeps
	// matching under errors.Is while the caller's log still carries the status
	// code and server message: a 404 (bad path / no access) and a 422 (not
	// enforced) then stay distinguishable instead of collapsing to one opaque
	// line.
	if gitlab.HasStatusCode(err, http.StatusNotFound) || gitlab.HasStatusCode(err, http.StatusUnprocessableEntity) {
		return Result{}, fmt.Errorf("%w: %w", errFirewallNotEvaluating, err)
	}

	if err != nil {
		return Result{}, fmt.Errorf("dependency firewall evaluation failed: %w", err)
	}

	return resultForOutcome(eval)
}

// ecosystemValue maps a coordinate ecosystem to the client-go enum, erroring
// on an unknown value rather than passing a free-form string that the API
// would reject with a 400. Normalizing at this seam keeps an unsupported
// ecosystem out of the request instead of turning it into an opaque
// fail-closed block. It switches on purl's exported type constants, the single
// owner of the supported-ecosystem list, so a newly supported PURL type cannot
// pass purl.Parse and then silently fail closed here.
func ecosystemValue(ecosystem string) (gitlab.DependencyFirewallEcosystemValue, error) {
	switch ecosystem {
	case purl.TypeNpm:
		return gitlab.DependencyFirewallEcosystemNPM, nil
	case purl.TypePypi:
		return gitlab.DependencyFirewallEcosystemPyPI, nil
	case purl.TypeMaven:
		return gitlab.DependencyFirewallEcosystemMaven, nil
	case purl.TypeGem:
		return gitlab.DependencyFirewallEcosystemGem, nil
	default:
		return "", fmt.Errorf("unsupported dependency firewall ecosystem %q", ecosystem)
	}
}

// resultForOutcome maps a package evaluation outcome to a Result, erroring on
// a missing body or an unknown outcome so the caller fails closed.
func resultForOutcome(eval *gitlab.PackageEvaluation) (Result, error) {
	if eval == nil {
		return Result{}, errors.New("dependency firewall returned no evaluation")
	}

	switch eval.Outcome {
	case gitlab.DependencyFirewallOutcomeAllowed:
		return Result{}, nil
	case gitlab.DependencyFirewallOutcomeWarned:
		return Result{Verdict: verdict.Warning, Reason: reasonText(eval.Reason)}, nil
	case gitlab.DependencyFirewallOutcomeBlocked:
		return Result{Verdict: verdict.Blocked, Reason: reasonText(eval.Reason)}, nil
	default:
		return Result{}, fmt.Errorf("unknown dependency firewall outcome %q", eval.Outcome)
	}
}

// reasonText dereferences the optional reason the firewall returns for a
// warned or blocked outcome, mapping a nil pointer to an empty string.
func reasonText(reason *string) string {
	if reason == nil {
		return ""
	}
	return *reason
}
