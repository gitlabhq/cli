package login

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

// Any non-blank username is accepted when the password is a personal access
// token: https://docs.gitlab.com/user/profile/personal_access_tokens/#use-git-over-https
const personalAccessTokenUsername = "glab"

type options struct {
	io        *iostreams.IOStreams
	config    func() config.Config
	apiClient func(repoHost string) (*api.Client, error)

	operation string
}

func NewCmdCredential(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		config:    f.Config,
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:    "git-credential",
		Args:   cobra.ExactArgs(1),
		Short:  "Implements Git credential helper manager.",
		Hidden: true,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	return cmd
}

func (o *options) complete(args []string) {
	o.operation = args[0]
}

func (o *options) validate() error {
	if o.operation != "get" {
		// Ignore unsupported operation
		return cmdutils.SilentError
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	expectedParams := map[string]string{}

	s := bufio.NewScanner(o.io.In)
	for s.Scan() {
		line := s.Text()
		if line == "" {
			break
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) < 2 {
			continue
		}
		key, value := parts[0], parts[1]
		if key == "url" {
			u, err := url.Parse(value)
			if err != nil {
				return err
			}
			expectedParams["protocol"] = u.Scheme
			expectedParams["host"] = u.Host
			expectedParams["path"] = u.Path
			expectedParams["username"] = u.User.Username()
			expectedParams["password"], _ = u.User.Password()
		} else {
			expectedParams[key] = value
		}
	}
	if err := s.Err(); err != nil {
		return err
	}

	if expectedParams["protocol"] != "https" && expectedParams["protocol"] != "http" {
		return cmdutils.SilentError
	}

	cfg := o.config()

	output := map[string]string{}

	host := expectedParams["host"]
	isOAuth2Cfg, _ := cfg.Get(host, "is_oauth2")
	jobToken, _ := cfg.Get(host, "job_token")
	token, _ := cfg.Get(host, "token")
	username, _ := cfg.Get(host, "user")

	switch {
	case isOAuth2Cfg == "true":
		// Trying to refresh access token
		apiClient, err := o.apiClient(host)
		if err != nil {
			return err
		}
		cred, err := apiClient.Credential(ctx)
		if err != nil {
			return fmt.Errorf("failed to resolve the credentials for %q: %w", host, err)
		}
		if cred.Kind != api.CredentialOAuth2 {
			return fmt.Errorf("%q is configured for OAuth, but its credential resolved as %s", host, cred.Kind)
		}

		// see https://docs.gitlab.com/api/oauth2/#access-git-over-https-with-access-token
		output["username"] = "oauth2"
		output["password"] = cred.Token
		if !cred.Expiry.IsZero() {
			output["password_expiry_utc"] = fmt.Sprintf("%d", cred.Expiry.UTC().Unix())
		}
		if cred.RefreshToken != "" {
			output["oauth_refresh_token"] = cred.RefreshToken
		}
	case jobToken != "":
		output["username"] = "gitlab-ci-token"
		// see https://docs.gitlab.com/ci/jobs/ci_job_token/#to-git-clone-a-private-projects-repository
		output["password"] = jobToken
	case token != "":
		if username == "" {
			// Legacy configs and failed username lookups leave no user on
			// record: https://gitlab.com/gitlab-org/cli/-/issues/8044
			username = personalAccessTokenUsername
		}
		output["username"] = username
		output["password"] = token
	default:
		return cmdutils.SilentError
	}

	if expectedParams["username"] != "" && expectedParams["username"] != output["username"] {
		return fmt.Errorf("the requested username by Git doesn't match the one that is configured for this host with GLab, want %q but got %q. Rejecting request", output["username"], expectedParams["username"])
	}

	// "A capability[] directive must precede any value depending on it and these directives should be the first item announced in the protocol." https://git-scm.com/docs/git-credential
	o.io.LogInfo("capability[]=authtype")
	for key, v := range output {
		o.io.LogInfof("%s=%s\n", key, v)
	}

	return nil
}

// detectedHost represents a GitLab hostname detected from git remotes
type detectedHost struct {
	hostname      string
	remotes       []string // Names of remotes pointing to this host
	score         int      // Priority score for sorting
	authenticated bool     // Whether user is already authenticated to this host
}

// detectGitLabHosts detects GitLab hostnames from git remotes in the current repository
func detectGitLabHosts(cfg config.Config) ([]detectedHost, error) {
	// Check if we're in a git repository
	_, err := git.ToplevelDir()
	if err != nil {
		// Not in a git repo, return empty list
		return nil, err
	}

	// Get git remotes
	gitRemotes, err := git.Remotes()
	if err != nil {
		return nil, err
	}

	if len(gitRemotes) == 0 {
		return nil, nil
	}

	// Get authenticated hosts from config
	var authenticatedHosts map[string]bool
	if hosts, err := cfg.Hosts(); err == nil {
		authenticatedHosts = make(map[string]bool, len(hosts))
		for _, host := range hosts {
			authenticatedHosts[host] = true
		}
	}

	// Group remotes by hostname
	// Pre-allocate for maximum possible unique hosts (one per remote)
	hostMap := make(map[string][]string, len(gitRemotes))
	for _, remote := range gitRemotes {
		hostname := extractHostFromRemote(remote)
		if hostname == "" {
			// Skip local file remotes (file://, relative paths, etc.)
			continue
		}
		hostMap[hostname] = append(hostMap[hostname], remote.Name)
	}

	// Convert to detectedHost slice
	detectedHosts := make([]detectedHost, 0, len(hostMap))
	for hostname, remoteNames := range hostMap {
		host := detectedHost{
			hostname:      hostname,
			remotes:       remoteNames,
			authenticated: authenticatedHosts[hostname],
		}
		detectedHosts = append(detectedHosts, host)
	}

	// Prioritize and sort
	detectedHosts = prioritizeHosts(detectedHosts)

	return detectedHosts, nil
}

// extractHostFromRemote extracts the hostname from a git remote
func extractHostFromRemote(remote *git.Remote) string {
	// Try FetchURL first, then PushURL
	var u *url.URL
	if remote.FetchURL != nil {
		u = remote.FetchURL
	} else if remote.PushURL != nil {
		u = remote.PushURL
	}

	if u == nil {
		// Both FetchURL and PushURL are nil (shouldn't happen with valid git remotes,
		// but possible if URL parsing failed in git.parseRemotes)
		return ""
	}

	return u.Host
}

// prioritizeHosts sorts detected hosts by priority score
func prioritizeHosts(hosts []detectedHost) []detectedHost {
	// Calculate scores
	for i := range hosts {
		hosts[i].score = calculateHostScore(&hosts[i])
	}

	// Sort by score (descending)
	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].score > hosts[j].score
	})

	return hosts
}

// calculateHostScore calculates a priority score for a detected host
func calculateHostScore(host *detectedHost) int {
	score := 0

	// Sum scores from all remotes pointing to this host
	for _, remoteName := range host.remotes {
		score += remoteNameScore(remoteName)
	}

	// Already authenticated hosts get high priority
	if host.authenticated {
		score += 10
	}

	// gitlab.com gets a boost as the default instance
	if host.hostname == glinstance.DefaultHostname {
		score += 5
	}

	return score
}

// remoteNameScore assigns a priority score based on remote name
func remoteNameScore(name string) int {
	switch strings.ToLower(name) {
	case "origin":
		return 3
	case "upstream":
		return 2
	case "gitlab":
		return 1
	default:
		return 0
	}
}

// String formats a detected host for display in survey prompt
func (h detectedHost) String() string {
	// Handle nil or empty remotes
	if len(h.remotes) == 0 {
		if h.authenticated {
			return fmt.Sprintf("%s [authenticated]", h.hostname)
		}
		return h.hostname
	}

	// Sort remote names for consistent display
	remotes := make([]string, len(h.remotes))
	copy(remotes, h.remotes)
	slices.Sort(remotes)

	result := fmt.Sprintf("%s (%s)", h.hostname, strings.Join(remotes, ", "))
	if h.authenticated {
		result += " [authenticated]"
	}
	return result
}
