package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/fsx"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/git"
)

// Summary is the enablement endpoint's answer for when GitLab can't be
// reached. Host and ProjectID keep a stored copy from applying to another
// project.
type Summary struct {
	Host                string    `json:"host"`
	ProjectID           string    `json:"project_id"`
	UnreachableFallback string    `json:"unreachable_fallback"`
	ExpiresAt           time.Time `json:"expires_at"`
}

// verdict maps the fallback to a verdict. Anything other than allow or warn,
// including an empty or unknown value from a newer server, blocks.
func (s *Summary) verdict() verdict.Verdict {
	switch s.UnreachableFallback {
	case "allow":
		return verdict.Allowed
	case "warn":
		return verdict.Warning
	default:
		return verdict.Blocked
	}
}

// applies checks expiry on every call, so a long run cannot outlive the
// window the server granted. It is nil safe.
func (s *Summary) applies(projectID string) bool {
	return s != nil && s.ProjectID == projectID && s.verdict() != verdict.Blocked && time.Now().Before(s.ExpiresAt)
}

func summaryPath(baseDir string) string {
	return filepath.Join(baseDir, ".gitlab", "df", "policy-summary.json")
}

// LoadSummary fetches the project's policy summary and stores it under
// baseDir. Only when the fetch gets no answer does it return the stored copy,
// and only if Git confirms it doesn't track it.
// It returns nil, leaving failed checks blocking, when there is no usable
// summary or checker is not the REST checker.
func LoadSummary(ctx context.Context, checker Checker, projectID, baseDir string, log Logf) *Summary {
	rest, ok := checker.(restChecker)
	if !ok {
		return nil
	}
	host := rest.client.BaseURL().Host
	path := summaryPath(baseDir)

	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	fresh, err := fetchSummary(ctx, rest.client, projectID)
	if err == nil {
		fresh.Host, fresh.ProjectID = host, projectID
		storeSummary(path, fresh, log)
		return fresh
	}
	if !isUnreachable(ctx, err) {
		dbg.Debugf("dependency firewall: could not fetch the policy summary: %v; no unreachable fallback for this run", err)
		return nil
	}

	stored, err := readSummary(path)
	if err != nil {
		if !os.IsNotExist(err) && log != nil {
			log("warning: dependency firewall could not read the stored policy summary: %v\n", err)
		}
		return nil
	}
	if stored.Host != host || stored.ProjectID != projectID {
		dbg.Debugf("dependency firewall: the stored policy summary is for %s %s, not %s %s; no unreachable fallback for this run", stored.Host, stored.ProjectID, host, projectID)
		return nil
	}
	// A committed copy outlives every checkout, and whoever commits it picks
	// its expiry, so only an untracked copy (a CI/CD cache or an earlier run) applies.
	tracked, err := trackedByGit(path)
	if err != nil {
		if log != nil {
			log("warning: dependency firewall ignored the stored policy summary %s because Git couldn't confirm it is untracked: %v\n", path, err)
		}
		return nil
	}
	if tracked {
		if log != nil {
			log("warning: dependency firewall ignored the stored policy summary %s because Git tracks it, and a committed summary isn't trusted\n", path)
		}
		return nil
	}
	return stored
}

// trackedByGit reports whether Git tracks path. It returns an error when Git
// can't tell, for example without git or outside a repository.
func trackedByGit(path string) (bool, error) {
	err := git.GitCommand("-C", filepath.Dir(path), "ls-files", "--error-unmatch", "--", filepath.Base(path)).Run()
	// Exit status 1 is the only answer that means untracked.
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

func fetchSummary(ctx context.Context, client *gitlab.Client, projectID string) (*Summary, error) {
	req, err := client.NewRequest(http.MethodGet,
		fmt.Sprintf("projects/%s/dependency_firewall/enablement", url.PathEscape(projectID)), nil,
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx)})
	if err != nil {
		return nil, err
	}
	var s Summary
	if _, err := client.Do(req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// storeSummary writes s to path. A failed write only costs a later run its
// fallback, so it is logged rather than failing this run.
func storeSummary(path string, s *Summary, log Logf) {
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		var data []byte
		data, err = json.Marshal(s) //nolint:forbidigo // serializing the policy summary to disk, not stdout output
		if err == nil {
			err = fsx.WriteOwnerOnly(path, append(data, '\n'))
		}
	}
	if err != nil && log != nil {
		log("warning: dependency firewall could not store the policy summary: %v\n", err)
	}
}

func readSummary(path string) (*Summary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Summary
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// isUnreachable is glab's own check, modeled on client-go v3.52.0's unexported
// retryHTTPCheck. It leaves out the isMethodRetryable branch (any error on a
// repeatable method) and the idempotent-only gate, so a reset mid-request on a
// GET doesn't count as unreachable.
func isUnreachable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errResp, ok := errors.AsType[*gitlab.ErrorResponse](err); ok {
		return errResp.HasStatusCode(http.StatusTooManyRequests) || errResp.HasStatusCode(0) ||
			(errResp.StatusCode >= http.StatusInternalServerError && !errResp.HasStatusCode(http.StatusNotImplemented))
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		return !dnsErr.IsNotFound
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		return opErr.Temporary() || strings.EqualFold(opErr.Op, "dial")
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return strings.Contains(urlErr.Error(), "connection refused")
	}
	if _, ok := errors.AsType[interface {
		error
		Timeout() bool
		Temporary() bool
	}](err); ok {
		return strings.Contains(err.Error(), "net/http: TLS handshake timeout")
	}
	return false
}
