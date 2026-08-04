package status

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	hostname  string
	showToken bool
	all       bool

	defaultHostname    string
	httpClientOverride func(token, hostname string) (*api.Client, error) // used in tests to mock http client
	io                 *iostreams.IOStreams
	apiClient          func(repoHost string) (*api.Client, error)
	config             func() config.Config
}

func NewCmdStatus(f cmdutils.Factory, runE func(*options) error) *cobra.Command {
	opts := &options{
		io:              f.IO(),
		apiClient:       f.ApiClient,
		config:          f.Config,
		defaultHostname: f.DefaultHostname(),
	}

	cmd := &cobra.Command{
		Use:   "status",
		Args:  cobra.ExactArgs(0),
		Short: "View authentication status.",
		Long: heredoc.Docf(`
		Verifies and displays information about your authentication state.

		By default, this command checks the authentication state of the GitLab instance
		determined by your current context (%[1]sgit remote%[1]s, %[1]sGITLAB_HOST%[1]s environment variable,
		or configuration). To check all configured instances, use %[1]s--all%[1]s.
		To check a specific instance, use %[1]s--hostname%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			# Check authentication status for the instance in your current context
			glab auth status

			# Check authentication status for all configured instances
			glab auth status --all

			# Check authentication status for a specific instance
			glab auth status --hostname gitlab.example.com

			# Display the authentication token alongside the status
			glab auth status --show-token
		`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if runE != nil {
				return runE(opts)
			}

			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVarP(&opts.hostname, "hostname", "", "", "Check the authentication status of a specific instance.")
	cmd.Flags().BoolVarP(&opts.showToken, "show-token", "t", false, "Display the authentication token.")
	cmd.Flags().BoolVarP(&opts.all, "all", "a", false, "Check the authentication status of all configured instances.")

	cmd.MarkFlagsMutuallyExclusive("all", "hostname")

	return cmd
}

func (o *options) run(ctx context.Context) error {
	c := o.io.Color()
	cfg := o.config()

	statusInfo := map[string][]string{}

	instances, err := cfg.Hosts()
	if len(instances) == 0 || err != nil {
		return fmt.Errorf("no GitLab instances have been authenticated with glab; run `%s` to authenticate", c.Bold("glab auth login"))
	}

	// Determine which host(s) to check
	// Priority: --hostname flag > --all flag > defaultHostname (from GITLAB_HOST/git remote)
	if o.hostname == "" && !o.all {
		// No explicit flags, use default hostname if it's not gitlab.com
		// This means GITLAB_HOST or git remote has set a specific host
		if o.defaultHostname != glinstance.DefaultHostname {
			o.hostname = o.defaultHostname
		}
		// else: hostname stays empty, will check all instances (backward compatible)
	}

	if o.hostname != "" && !slices.Contains(instances, o.hostname) {
		return fmt.Errorf("%s %s has not been authenticated with glab; run `%s %s` to authenticate", c.FailedIcon(), o.hostname, c.Bold("glab auth login --hostname"), c.Bold(o.hostname))
	}

	failedAuth := false
	for _, instance := range instances {
		if o.hostname != "" && o.hostname != instance {
			continue
		}
		statusInfo[instance] = []string{}
		addMsg := func(x string, ys ...any) {
			statusInfo[instance] = append(statusInfo[instance], fmt.Sprintf(x, ys...))
		}

		token, tokenSource, tokenErr := cfg.GetWithSource(instance, "token", true)
		apiClient, err := o.apiClient(instance)
		if o.httpClientOverride != nil {
			apiClient, _ = o.httpClientOverride(token, instance)
		}
		switch {
		case tokenErr != nil:
			// A read failure here is a real keyring error (locked, denied, or
			// unavailable); surface it instead of proceeding with an empty token
			// and a confusing 401.
			failedAuth = true
			addMsg("%s %s: could not read the token: %s", c.FailedIcon(), instance, tokenErr)
		case err == nil:
			authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			user, resp, err := apiClient.Lab().Users.CurrentUser(gitlab.WithContext(authCtx))
			cancel()
			if err != nil {
				failedAuth = true
				addMsg("%s %s: API call failed: %s", c.FailedIcon(), instance, sanitizeAuthError(err))
				if resp != nil && resp.StatusCode == 401 && slices.Contains(config.EnvKeyEquivalence("token"), tokenSource) {
					addMsg("  %s Token is from environment variable %s. A wrapper may be injecting a different or expired token.", c.WarnIcon(), tokenSource)
					addMsg("  %s To investigate, run %s: an alias such as 'op plugin run -- glab' means a wrapper (for example, a 1Password shell plugin) is injecting the token; a plain path rules that out.", c.WarnIcon(), c.Bold("type glab"))
					addMsg("  %s To see the token value in use, run: %s", c.WarnIcon(), c.Bold("env | grep -E 'GITLAB_TOKEN|GITLAB_ACCESS_TOKEN|OAUTH_TOKEN'"))
				}
			} else {
				addMsg("%s Logged in to %s as %s (%s)", c.GreenCheck(), instance, c.Bold(user.Username), tokenSource)
			}
		default:
			failedAuth = true
			addMsg("%s %s: failed to initialize api client: %s", c.FailedIcon(), instance, err)
		}
		proto, _ := cfg.Get(instance, "git_protocol")
		if proto != "" {
			addMsg("%s Git operations for %s configured to use %s protocol.",
				c.GreenCheck(), instance, c.Bold(proto))
		}
		apiProto, _ := cfg.Get(instance, "api_protocol")
		apiHost, _ := cfg.Get(instance, "api_host")
		subfolder, _ := cfg.Get(instance, "subfolder")
		sshHost, _ := cfg.Get(instance, "ssh_host")
		apiEndpoint := glinstance.APIEndpoint(instance, apiProto, apiHost, subfolder)
		graphQLEndpoint := glinstance.GraphQLEndpoint(instance, apiProto, apiHost, subfolder)
		if apiProto != "" {
			addMsg("%s API calls for %s are made over %s protocol.",
				c.GreenCheck(), instance, c.Bold(apiProto))
			addMsg("%s REST API Endpoint: %s",
				c.GreenCheck(), c.Bold(apiEndpoint))
			addMsg("%s GraphQL Endpoint: %s",
				c.GreenCheck(), c.Bold(graphQLEndpoint))
		}
		if subfolder != "" {
			addMsg("%s Subfolder: %s", c.GreenCheck(), c.Bold(subfolder))
		}
		if sshHost != "" {
			addMsg("%s SSH Host: %s", c.GreenCheck(), c.Bold(sshHost))
		}
		// Skip the token-presence report when the read itself failed: the error
		// is already shown above, and "No token found" would be misleading since
		// the token was not simply absent.
		if tokenErr == nil {
			if api.IsTokenConfigured(token) {
				tokenDisplay := "**************************"
				if o.showToken {
					tokenDisplay = token
				}
				addMsg("%s Token found in %s: %s", c.GreenCheck(), tokenStorageDescription(tokenSource), tokenDisplay)

				// Nudge users whose credentials are still stored as plaintext in the
				// config file toward the keyring, which is now the default on login.
				if isPlaintextTokenSource(tokenSource) {
					addMsg("%s To store this token more securely, run %s to move it into the operating system keyring.",
						c.WarnIcon(), c.Bold("glab auth login --hostname "+instance))
				}
			} else {
				addMsg("%s No token found (checked config file, keyring, and environment variables).", c.WarnIcon())
			}
		}
	}

	for _, instance := range instances {
		if o.hostname != "" && o.hostname != instance {
			continue
		}

		lines, ok := statusInfo[instance]
		if !ok {
			continue
		}
		o.io.LogErrorf("%s\n", c.Bold(instance))
		for _, line := range lines {
			o.io.LogErrorf("  %s\n", line)
		}
	}

	envToken, envTokenSource := config.GetFromEnvWithSource("token")
	if envToken != "" {
		o.io.LogErrorf("\n%s Token is from environment variable %s. This takes precedence over tokens stored in config or keyring.\n", c.WarnIcon(), envTokenSource)
		o.io.LogErrorf("  Run %s to find the source: an alias such as 'op plugin run -- glab' means a wrapper (for example, a 1Password shell plugin) is injecting it, which is expected and needs no action.\n", c.Bold("type glab"))
		o.io.LogErrorf("  A plain path means it is set in your environment (for example, a shell profile such as ~/.bashrc or ~/.zshrc, or a CI/CD variable); remove it there so glab uses your stored credentials.\n")
	}

	if failedAuth {
		return fmt.Errorf("\n%s could not authenticate to one or more of the configured GitLab instances", c.FailedIcon())
	} else {
		return nil
	}
}

// sanitizeAuthError returns a concise message for authentication errors. When
// the OAuth token endpoint returns a non-2XX response without a structured
// error (for example, an HTML 500 error page), golang.org/x/oauth2 embeds the
// entire response body in the error string, which floods the terminal with
// unhelpful markup. In that case, replace the body-containing portion with a
// message that reports only the HTTP status, while preserving any surrounding
// context added by an outer wrapper. Structured OAuth errors and all other
// errors are already concise, so they are returned unchanged.
func sanitizeAuthError(err error) string {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "" {
		status := "unknown status"
		if retrieveErr.Response != nil {
			status = retrieveErr.Response.Status
		}
		sanitized := fmt.Sprintf("oauth2: cannot fetch token: %s", status)
		// A nil Response would make retrieveErr.Error() (and thus err.Error())
		// panic, so return the sanitized message directly in that case.
		// Otherwise swap only the noisy inner error string so that context from
		// any outer wrapper (e.g. "token refresh: ...") is retained.
		if retrieveErr.Response == nil {
			return sanitized
		}
		return strings.Replace(err.Error(), retrieveErr.Error(), sanitized, 1)
	}
	return err.Error()
}

// keyringTokenSource is the source label returned by the config layer when a
// token is read from the operating system keyring.
const keyringTokenSource = "keyring"

// tokenStorageDescription returns a human-readable description of where a
// token was read from, for display in status output.
func tokenStorageDescription(tokenSource string) string {
	switch {
	case tokenSource == keyringTokenSource:
		return "operating system keyring"
	case slices.Contains(config.EnvKeyEquivalence("token"), tokenSource):
		return "environment variable " + tokenSource
	default:
		return "configuration file (plaintext)"
	}
}

// isPlaintextTokenSource reports whether the token was read from the plaintext
// configuration file (rather than the keyring or an environment variable), in
// which case status suggests migrating it to the keyring.
func isPlaintextTokenSource(tokenSource string) bool {
	if tokenSource == keyringTokenSource {
		return false
	}
	return !slices.Contains(config.EnvKeyEquivalence("token"), tokenSource)
}
