package status

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

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
		Long: heredoc.Docf(`Verifies and displays information about your authentication state.

		By default, this command checks the authentication state of the GitLab instance
		determined by your current context (%[1]sgit remote%[1]s, %[1]sGITLAB_HOST%[1]s environment variable,
		or configuration). Use %[1]s--all%[1]s to check all configured instances, or %[1]s--hostname%[1]s to
		check a specific instance.
		`, "`"),
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

	cmd.Flags().StringVarP(&opts.hostname, "hostname", "", "", "Check a specific instance's authentication status.")
	cmd.Flags().BoolVarP(&opts.showToken, "show-token", "t", false, "Display the authentication token. (default false)")
	cmd.Flags().BoolVarP(&opts.all, "all", "a", false, "Check all configured instances. (default false)")

	cmd.MarkFlagsMutuallyExclusive("all", "hostname")

	return cmd
}

func (o *options) run(ctx context.Context) error {
	c := o.io.Color()
	cfg := o.config()

	stderr := o.io.StdErr

	statusInfo := map[string][]string{}

	instances, err := cfg.Hosts()
	if len(instances) == 0 || err != nil {
		return fmt.Errorf("No GitLab instances have been authenticated with glab. Run `%s` to authenticate.\n", c.Bold("glab auth login"))
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
		return fmt.Errorf("%s %s has not been authenticated with glab. Run `%s %s` to authenticate.", c.FailedIcon(), o.hostname, c.Bold("glab auth login --hostname"), c.Bold(o.hostname))
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

		token, tokenSource, _ := cfg.GetWithSource(instance, "token", true)
		apiClient, err := o.apiClient(instance)
		if o.httpClientOverride != nil {
			apiClient, _ = o.httpClientOverride(token, instance)
		}
		if err == nil {
			authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			user, resp, err := apiClient.Lab().Users.CurrentUser(gitlab.WithContext(authCtx))
			cancel()
			if err != nil {
				failedAuth = true
				addMsg("%s %s: API call failed: %s", c.FailedIcon(), instance, err)
				if resp != nil && resp.StatusCode == 401 && slices.Contains(config.EnvKeyEquivalence("token"), tokenSource) {
					addMsg("  %s Token is from environment variable %s. A wrapper may be injecting a different or expired token.", c.WarnIcon(), tokenSource)
					addMsg("  %s To investigate, run in your shell: %s", c.WarnIcon(), c.Bold("type glab"))
					addMsg("  %s To see the token value in use, run: %s", c.WarnIcon(), c.Bold("env | grep -E 'GITLAB_TOKEN|GITLAB_ACCESS_TOKEN|OAUTH_TOKEN'"))
				}
			} else {
				addMsg("%s Logged in to %s as %s (%s)", c.GreenCheck(), instance, c.Bold(user.Username), tokenSource)
			}
		} else {
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
		if api.IsTokenConfigured(token) {
			tokenDisplay := "**************************"
			if o.showToken {
				tokenDisplay = token
			}
			addMsg("%s Token found: %s", c.GreenCheck(), tokenDisplay)
		} else {
			addMsg("%s No token found (checked config file, keyring, and environment variables).", c.WarnIcon())
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
		fmt.Fprintf(stderr, "%s\n", c.Bold(instance))
		for _, line := range lines {
			fmt.Fprintf(stderr, "  %s\n", line)
		}
	}

	envToken, envTokenSource := config.GetFromEnvWithSource("token")
	if envToken != "" {
		fmt.Fprintf(stderr, "\n%s Token is from environment variable %s. This takes precedence over tokens stored in config or keyring.\n", c.WarnIcon(), envTokenSource)
		fmt.Fprintf(stderr, "  If a wrapper (e.g., 'op plugin run -- glab') is setting this, run %s in your shell to check.\n", c.Bold("type glab"))
	}

	if failedAuth {
		return fmt.Errorf("\n%s could not authenticate to one or more of the configured GitLab instances.", c.FailedIcon())
	} else {
		return nil
	}
}
