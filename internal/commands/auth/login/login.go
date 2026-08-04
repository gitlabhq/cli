package login

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/auth/authutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
)

type LoginOptions struct {
	IO              *iostreams.IOStreams
	Config          func() config.Config
	apiClient       func(repoHost string) (*api.Client, error)
	defaultHostname string

	Interactive bool

	Hostname string
	Token    string
	JobToken string

	ApiHost                  string
	ApiProtocol              string
	GitProtocol              string
	SSHHostname              string
	ContainerRegistryDomains string

	WebLogin        bool
	DeviceLogin     bool
	UseKeyring      bool // Deprecated: keyring storage is now the default.
	InsecureStorage bool
}

var opts *LoginOptions

func NewCmdLogin(f cmdutils.Factory) *cobra.Command {
	opts = &LoginOptions{
		IO:              f.IO(),
		Config:          f.Config,
		apiClient:       f.ApiClient,
		defaultHostname: f.DefaultHostname(),
	}

	var tokenStdin bool

	cmd := &cobra.Command{
		Use:   "login",
		Args:  cobra.ExactArgs(0),
		Short: "Authenticate with a GitLab instance.",
		Long: heredoc.Docf(`
			Authenticates with a GitLab instance.

			By default, glab stores your credentials in your operating system's
			keyring (macOS Keychain, Windows Credential Manager, or the Secret
			Service on Linux) when one is available. If no keyring is available,
			or if you pass %[1]s--insecure-storage%[1]s, glab stores them in the global
			configuration file (default %[1]s~/.config/glab-cli/config.yml%[1]s) as
			plaintext instead. After authentication, all glab commands use the
			stored credentials.

			If you previously signed in and your credentials are stored as
			plaintext in the configuration file, run %[1]sglab auth login%[1]s again to
			move them into the keyring.

			In CI (when %[1]sGITLAB_CI%[1]s or %[1]sCI%[1]s is set), glab stores credentials in the
			configuration file rather than the keyring. Credentials in CI are
			usually supplied through environment variables, and an OS keyring is
			often unavailable there.

			If %[1]sGITLAB_TOKEN%[1]s, %[1]sGITLAB_ACCESS_TOKEN%[1]s, or %[1]sOAUTH_TOKEN%[1]s are set,
			they take precedence over the stored credentials. When CI auto-login is
			enabled, these variables also override %[1]sCI_JOB_TOKEN%[1]s.

			To pass a token on standard input, use %[1]s--stdin%[1]s.

			In interactive mode, glab detects GitLab instances from your Git remotes
			and lists them as options, so you do not have to type the hostname manually.
		`, "`"),
		Example: heredoc.Docf(`
			# Start interactive setup
			# If in a Git repository, glab detects and suggests GitLab instances from remotes
			glab auth login

			# Authenticate against %[1]sgitlab.com%[1]s by reading the token from a file
			glab auth login --stdin < myaccesstoken.txt

			# Authenticate with GitLab Self-Managed or GitLab Dedicated
			glab auth login --hostname salsa.debian.org

			# Non-interactive setup
			glab auth login --hostname gitlab.example.org --token glpat-xxx --api-host gitlab.example.org:3443 --api-protocol https --git-protocol ssh

			# Non-interactive setup reading the token from a file
			glab auth login --hostname gitlab.example.org --api-host gitlab.example.org:3443 --api-protocol https --git-protocol ssh --stdin < myaccesstoken.txt

			# Semi-interactive OAuth login, skipping all prompts except browser auth
			glab auth login --hostname gitlab.com --web --git-protocol ssh --container-registry-domains "gitlab.com,gitlab.com:443,registry.gitlab.com"

			# OAuth device authorization flow for headless environments without a local browser.
			# glab displays a one-time code and verification URL; you authorize on any
			# other device with a browser. Requires GitLab 17.9 or later.
			glab auth login --hostname gitlab.com --device

			# CI/CD setup: for most cases, prefer auto-login over manual login
			GLAB_ENABLE_CI_AUTOLOGIN=true glab release list -R $CI_PROJECT_PATH

			# CI/CD setup with manual login: use when the command does not support CI job tokens, or you need a personal access token
			glab auth login --hostname $CI_SERVER_FQDN --job-token $CI_JOB_TOKEN --api-protocol $CI_SERVER_PROTOCOL
		`, "`"),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !opts.IO.PromptEnabled() && !tokenStdin && opts.Token == "" && opts.JobToken == "" && !opts.WebLogin && !opts.DeviceLogin {
				return &cmdutils.FlagError{Err: errors.New("'--stdin', '--token', '--job-token', '--web', or '--device' required when not running interactively")}
			}

			// --token, --stdin, --job-token, --web, --device are pairwise mutually
			// exclusive via cmd.MarkFlagsMutuallyExclusive below.

			if tokenStdin {
				defer opts.IO.In.Close()
				token, err := io.ReadAll(opts.IO.In)
				if err != nil {
					return fmt.Errorf("failed to read token from STDIN: %w", err)
				}
				opts.Token = strings.TrimSpace(string(token))
			}

			if opts.IO.PromptEnabled() && opts.Token == "" && opts.JobToken == "" && opts.IO.IsaTTY {
				opts.Interactive = true
			}

			if cmd.Flags().Changed("hostname") {
				if err := hostnameValidator(opts.Hostname); err != nil {
					return &cmdutils.FlagError{Err: fmt.Errorf("error parsing '--hostname': %w", err)}
				}
			}

			if cmd.Flags().Changed("api-host") && strings.Contains(opts.ApiHost, "://") {
				stripped, _ := glinstance.StripHostProtocol(opts.ApiHost)
				if stripped == "" {
					return &cmdutils.FlagError{Err: fmt.Errorf("error parsing '--api-host': value must be a hostname, not a URL (for example, %q or %q)", "example.com", "example.com:3443")}
				}
				return &cmdutils.FlagError{Err: fmt.Errorf("error parsing '--api-host': value must be a hostname, not a URL. Use %q instead", stripped)}
			}

			if !opts.Interactive && opts.Hostname == "" {
				opts.Hostname = glinstance.DefaultHostname
			}

			// Note: --api-host, --api-protocol, --git-protocol are now allowed
			// in interactive mode. When set, they skip the corresponding prompts
			// instead of erroring. This enables semi-interactive login flows.

			if err := loginRun(cmd.Context(), opts); err != nil {
				return cmdutils.WrapError(err, "Could not sign in!")
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&opts.Hostname, "hostname", "", "", "The hostname of the GitLab instance to authenticate with.")
	cmd.Flags().StringVarP(&opts.Token, "token", "t", "", "Your GitLab access token.")
	cmd.Flags().StringVarP(&opts.JobToken, "job-token", "j", "", "CI job token.")
	cmd.Flags().BoolVar(&tokenStdin, "stdin", false, "Read the token from standard input.")
	cmd.Flags().BoolVar(&opts.UseKeyring, "use-keyring", false, "Store the token in your operating system's keyring.")
	cmd.Flags().BoolVar(&opts.InsecureStorage, "insecure-storage", false, "Store the token as plaintext in the configuration file instead of the operating system's keyring.")
	cobra.CheckErr(cmd.Flags().MarkDeprecated("use-keyring", "keyring storage is now the default. Use --insecure-storage to store the token in the configuration file."))
	cmd.Flags().BoolVar(&opts.WebLogin, "web", false, "Skip the login type prompt and use web/OAuth login.")
	cmd.Flags().BoolVar(&opts.DeviceLogin, "device", false, "Use the OAuth 2.0 device authorization flow. Useful for headless environments where a local browser is not available. Requires GitLab 17.9 or later.")
	cmd.Flags().StringVarP(&opts.ApiHost, "api-host", "a", "", "Hostname for the API endpoint, if different from --hostname. Accepts a hostname or hostname:port. Use only when the API is served from a different host than the Git remote.")
	cmd.Flags().StringVarP(&opts.ApiProtocol, "api-protocol", "p", "", "API protocol. Options: https, http.")
	cmd.Flags().StringVarP(&opts.GitProtocol, "git-protocol", "g", "", "Git protocol. Options: ssh, https, http.")
	cmd.Flags().StringVar(&opts.SSHHostname, "ssh-hostname", "", "SSH hostname for instances with a different SSH endpoint. A port is not required; Git uses the port from the remote URL.")
	cmd.Flags().StringVar(&opts.ContainerRegistryDomains, "container-registry-domains", "", "Container registry and image dependency proxy domains, comma-separated.")

	cmd.MarkFlagsMutuallyExclusive("token", "stdin", "job-token", "web", "device")
	cmd.MarkFlagsMutuallyExclusive("use-keyring", "insecure-storage")

	return cmd
}

// lookupUsername resolves the account behind the token that was just stored on
// hostname, so `glab auth status` and the Git credential helper can use the
// real username.
func lookupUsername(ctx context.Context, opts *LoginOptions, hostname string) (string, error) {
	apiClient, err := opts.apiClient(hostname)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	user, _, err := apiClient.Lab().Users.CurrentUser(gitlab.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

func loginRun(ctx context.Context, opts *LoginOptions) error {
	c := opts.IO.Color()
	cfg := opts.Config()

	// Decide once whether credentials should be stored in the OS keyring.
	// Keyring is the default when a backend is available. --insecure-storage
	// forces plaintext file storage. The deprecated --use-keyring only still
	// matters in CI, where it opts back in to the keyring. The resolved value
	// is persisted per-host in each authentication path below.
	keyringPref := "false"
	switch {
	case opts.InsecureStorage:
		// Explicit opt-out: store in the configuration file without warning.
	case !opts.UseKeyring && config.InCI():
		// In CI, credentials are ephemeral and usually supplied via environment
		// variables, and an OS keyring is typically unavailable or not persisted
		// across job steps (a locked keyring would also make later reads fail).
		// Default to file storage there unless the keyring is explicitly
		// requested with the (deprecated) --use-keyring.
	case config.KeyringAvailable():
		keyringPref = "true"
	default:
		// No keyring backend available: fall back to file storage, but warn that
		// credentials are stored as plaintext.
		opts.IO.LogErrorf("%s The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file.\n", c.Yellow("WARNING:"))
	}

	if opts.Token != "" {
		if opts.Hostname == "" {
			return errors.New("empty hostname would leak `oauth_token`")
		}

		// Split hostname and subfolder
		hostname, subfolder := splitHostnameAndSubfolder(opts.Hostname)

		if err := authutils.ClearAuthFields(cfg, hostname); err != nil {
			return err
		}

		if err := cfg.Set(hostname, "use_keyring", keyringPref); err != nil {
			return err
		}

		err := cfg.Set(hostname, "token", opts.Token)
		if err != nil {
			return err
		}

		if opts.ApiHost != "" {
			err := cfg.Set(hostname, "api_host", opts.ApiHost)
			if err != nil {
				return err
			}
		}

		if subfolder != "" {
			err := cfg.Set(hostname, "subfolder", subfolder)
			if err != nil {
				return err
			}
		}

		if opts.ApiProtocol != "" {
			err := cfg.Set(hostname, "api_protocol", opts.ApiProtocol)
			if err != nil {
				return err
			}
		}

		if opts.GitProtocol != "" {
			err := cfg.Set(hostname, "git_protocol", opts.GitProtocol)
			if err != nil {
				return err
			}
		}

		// Best-effort: a failed lookup must not make this flow require network
		// access, because it is the flow scripts and CI jobs use.
		if username, err := lookupUsername(ctx, opts, hostname); err != nil {
			opts.IO.LogErrorf("%s Could not look up the username for this token: %v\n", c.Yellow("WARNING:"), err)
		} else {
			if err := cfg.Set(hostname, "user", username); err != nil {
				return err
			}
			opts.IO.LogErrorf("%s Logged in as %s\n", c.GreenCheck(), c.Bold(username))
		}

		if err := cfg.Write(); err != nil {
			return err
		}
		logCredentialStorage(opts.IO, keyringPref)
		warnEnvTokenPrecedence(opts.IO)
		return nil
	}

	if opts.JobToken != "" {
		if opts.Hostname == "" {
			return errors.New("empty hostname would leak `oauth_token`")
		}

		// Split hostname and subfolder
		hostname, subfolder := splitHostnameAndSubfolder(opts.Hostname)

		if err := authutils.ClearAuthFields(cfg, hostname); err != nil {
			return err
		}

		if err := cfg.Set(hostname, "use_keyring", keyringPref); err != nil {
			return err
		}

		err := cfg.Set(hostname, "job_token", opts.JobToken)
		if err != nil {
			return err
		}

		if opts.ApiHost != "" {
			err := cfg.Set(hostname, "api_host", opts.ApiHost)
			if err != nil {
				return err
			}
		}

		if subfolder != "" {
			err := cfg.Set(hostname, "subfolder", subfolder)
			if err != nil {
				return err
			}
		}

		if opts.ApiProtocol != "" {
			err := cfg.Set(hostname, "api_protocol", opts.ApiProtocol)
			if err != nil {
				return err
			}
		}

		if opts.GitProtocol != "" {
			err := cfg.Set(hostname, "git_protocol", opts.GitProtocol)
			if err != nil {
				return err
			}
		}

		if err := cfg.Write(); err != nil {
			return err
		}
		logCredentialStorage(opts.IO, keyringPref)
		return nil
	}

	// Split hostname into base hostname and subfolder if present
	var subfolder string
	hostname, _ := splitHostnameAndSubfolder(opts.Hostname)
	apiHostname := initialAPIHostname(cfg, hostname, opts.ApiHost)

	isSelfHosted := false

	if hostname == "" {
		// Try to detect GitLab hosts from git remotes
		detectedHosts, detectErr := detectGitLabHosts(cfg)

		if detectErr == nil && len(detectedHosts) > 0 {
			// We have detected hosts, present them to the user
			options := make([]string, 0, len(detectedHosts)+1)
			for _, host := range detectedHosts {
				options = append(options, host.String())
			}
			options = append(options, promptLoginDifferentHostname)

			var selectedOption string
			err := opts.IO.Select(ctx, &selectedOption, "Found GitLab instances in git remotes. Select one:", options)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			// Check if user selected "Enter a different hostname"
			if selectedOption == promptLoginDifferentHostname {
				// Fall back to manual entry
				var sshHostname string
				var err error
				hostname, apiHostname, sshHostname, err = promptForSelfHostedInstance(ctx, opts)
				if err != nil {
					return err
				}
				opts.SSHHostname = sshHostname
			} else {
				// User selected a detected host - find it in the list
				for _, host := range detectedHosts {
					if host.String() == selectedOption {
						hostname = host.hostname
						apiHostname = hostname
						break
					}
				}
			}
		} else {
			// No detected hosts or detection failed, fall back to original behavior
			options := []string{}
			if hosts, err := cfg.Hosts(); err == nil {
				options = append(options, hosts...)
			}
			if !slices.Contains(options, opts.defaultHostname) {
				options = append(options, opts.defaultHostname)
			}
			options = append(options, promptSelfManagedOrDedicatedInstance)

			var selectedOption string
			err := opts.IO.Select(ctx, &selectedOption, "What GitLab instance do you want to sign in to?", options)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			isSelfHosted = selectedOption == promptSelfManagedOrDedicatedInstance

			if isSelfHosted {
				var sshHostname string
				var err error
				hostname, apiHostname, sshHostname, err = promptForSelfHostedInstance(ctx, opts)
				if err != nil {
					return err
				}
				opts.SSHHostname = sshHostname
			} else {
				hostname = selectedOption
				apiHostname = hostname
			}
		}
	} else {
		isSelfHosted = glinstance.IsSelfHosted(hostname)

		// If interactive and self-hosted, prompt for API hostname and SSH hostname
		if opts.Interactive && isSelfHosted {
			// Prompt for API hostname (pre-filled by initialAPIHostname above)
			apiHostnameInput := huh.NewInput().
				Title("API hostname:").
				Description("For instances with a different hostname for the API endpoint.").
				Value(&apiHostname).
				Placeholder(hostname).
				Validate(func(s string) error {
					return hostnameValidator(s)
				})
			err := opts.IO.Run(ctx, apiHostnameInput)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			// Prompt for SSH hostname
			sshHostname := initialSSHHostname(cfg, hostname)

			sshHostnameInput := huh.NewInput().
				Title("SSH hostname:").
				Description("For instances with a different hostname for SSH git operations.").
				Value(&sshHostname).
				Placeholder(hostname).
				Validate(func(s string) error {
					return hostnameValidator(s)
				})
			err = opts.IO.Run(ctx, sshHostnameInput)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			opts.SSHHostname = sshHostname
		}
	}

	opts.IO.LogErrorf("- Signing into %s\n", hostname)

	// Surface the env-token precedence before the prompts below (the
	// "already logged in" confirmation and the sign-in method selection),
	// since it should inform the user's choice.
	warnEnvTokenPrecedence(opts.IO)

	existingToken, _, _ := cfg.GetWithSource(hostname, "token", false)

	if existingToken != "" && opts.Interactive {
		apiClient, err := opts.apiClient(hostname)
		if err != nil {
			return err
		}

		authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		user, _, err := apiClient.Lab().Users.CurrentUser(gitlab.WithContext(authCtx))
		if err == nil {
			username := user.Username
			keepGoing := false // default value
			confirm := huh.NewConfirm().
				Title(fmt.Sprintf(
					"You're already logged into %s as %s. Do you want to re-authenticate?",
					hostname,
					username)).
				Value(&keepGoing)
			err = opts.IO.Run(ctx, confirm)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			if !keepGoing {
				return nil
			}
		}
	}

	var (
		loginType                string
		containerRegistryDomains string
	)

	if opts.Interactive {
		switch {
		case opts.WebLogin:
			loginType = promptLoginTypeWeb
		case opts.DeviceLogin:
			loginType = promptLoginTypeDevice
		default:
			loginTypeOptions := []string{promptLoginTypeToken, promptLoginTypeWeb, promptLoginTypeDevice}
			err := opts.IO.Select(ctx, &loginType, "How would you like to sign in?", loginTypeOptions)
			if err != nil {
				return fmt.Errorf("could not get sign-in type: %w", err)
			}
		}

		if opts.ContainerRegistryDomains != "" {
			containerRegistryDomains = opts.ContainerRegistryDomains
		} else {
			containerRegistryDomains = initialContainerRegistryDomains(cfg, hostname)
			containerRegistryInput := huh.NewInput().
				Title("What domains does this host use for the container registry and image dependency proxy?").
				Value(&containerRegistryDomains).
				Placeholder(defaultContainerRegistryDomainsString(hostname))
			err := opts.IO.Run(ctx, containerRegistryInput)
			if err != nil {
				return fmt.Errorf("could not get container registry domains: %w", err)
			}
		}
	} else if opts.WebLogin {
		// Non-interactive web login: go straight to OAuth
		loginType = promptLoginTypeWeb
	} else if opts.DeviceLogin {
		// Non-interactive device flow login
		loginType = promptLoginTypeDevice
	}

	// Re-split hostname in case it was changed by prompts
	hostname, subfolder = splitHostnameAndSubfolder(hostname)

	// Persist the keyring preference before acquiring the token so that the
	// token (including tokens written by the OAuth flow's marshal step) is
	// routed to the correct storage backend.
	if hostname != "" {
		if err := cfg.Set(hostname, "use_keyring", keyringPref); err != nil {
			return err
		}
	}

	var token string
	var err error
	if strings.EqualFold(loginType, promptLoginTypeToken) {
		token, err = showTokenPrompt(ctx, opts.IO, hostname)
		if err != nil {
			return err
		}

		// Clear stale OAuth fields only after the prompt succeeds, so that a
		// cancelled or failed login leaves existing credentials intact.
		// This handles the OAuth → PAT switch: is_oauth2 / refresh / expiry fields
		// that were set by a previous OAuth login are removed before the PAT is saved.
		if err := authutils.ClearAuthFields(cfg, hostname); err != nil {
			return err
		}
	} else {
		client, err := opts.apiClient(hostname)
		if err != nil {
			return err
		}

		// StartFlow / StartDeviceFlow both call marshal() internally, which writes
		// is_oauth2, token, oauth2_refresh_token, and oauth2_expiry_date.  No
		// explicit ClearAuthFields is needed: marshal() overwrites every field it
		// owns, and is_oauth2=true ensures the OAuth auth source wins over any
		// residual job_token.
		if strings.EqualFold(loginType, promptLoginTypeDevice) {
			token, err = oauth2.StartDeviceFlow(ctx, cfg, opts.IO.StdErr, client.HTTPClient(), hostname)
		} else {
			token, err = oauth2.StartFlow(ctx, cfg, opts.IO.StdErr, client.HTTPClient(), hostname)
		}
		if err != nil {
			return err
		}
	}

	if err := cfg.Set(hostname, "token", token); err != nil {
		return err
	}

	if err := setContainerRegistryDomains(cfg, hostname, containerRegistryDomains); err != nil {
		return err
	}

	if hostname == "" {
		return errors.New("empty hostname would leak the token")
	}

	if err := cfg.Set(hostname, "api_host", apiHostname); err != nil {
		return err
	}

	// Set subfolder if present
	if subfolder != "" {
		if err := cfg.Set(hostname, "subfolder", subfolder); err != nil {
			return err
		}
	}

	// Set SSH hostname if it's different from the main hostname
	if opts.SSHHostname != "" && opts.SSHHostname != hostname {
		if err := cfg.Set(hostname, "ssh_host", opts.SSHHostname); err != nil {
			return err
		}
	}

	// Smart default: use SSH if user configured a custom SSH host
	gitProtocol := "https"
	if opts.SSHHostname != "" && opts.SSHHostname != hostname {
		gitProtocol = "ssh"
	}
	apiProtocol := "https"

	glabExecutable := "glab"
	if exe, err := os.Executable(); err == nil {
		glabExecutable = exe
	}
	credentialFlow := &authutils.GitCredentialFlow{Executable: glabExecutable}

	// Persist --git-protocol / --api-protocol before the interactive block so
	// the flags aren't silently dropped when stdin isn't a TTY (--web /
	// --device in CI or a devcontainer setup script). The interactive block
	// below also writes, but idempotently with the same value.
	if err := persistProtocolFlags(cfg, hostname, opts.GitProtocol, opts.ApiProtocol); err != nil {
		return err
	}

	if opts.Interactive {
		if opts.GitProtocol != "" {
			gitProtocol = strings.ToLower(opts.GitProtocol)
		} else {
			gitProtocolOptions := []string{promptProtocolSSH, promptProtocolHTTPS, promptProtocolHTTP}
			// Use smart default based on SSH hostname configuration
			defaultProtocol := promptProtocolHTTPS
			if gitProtocol == "ssh" {
				defaultProtocol = promptProtocolSSH
			}
			gitProtocol = defaultProtocol
			err = opts.IO.Select(ctx, &gitProtocol, "Choose default Git protocol:", gitProtocolOptions)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			gitProtocol = strings.ToLower(gitProtocol)
		}

		if gitProtocol != "ssh" {
			if err := credentialFlow.Prompt(ctx, opts.IO, hostname, gitProtocol); err != nil {
				return err
			}
		}

		if isSelfHosted {
			if opts.ApiProtocol != "" {
				apiProtocol = strings.ToLower(opts.ApiProtocol)
			} else {
				apiProtocolOptions := []string{promptProtocolHTTPS, promptProtocolHTTP}
				apiProtocol = promptProtocolHTTPS // Set default
				err = opts.IO.Select(ctx, &apiProtocol, "Choose host API protocol:", apiProtocolOptions)
				if err != nil {
					return fmt.Errorf("could not prompt: %w", err)
				}

				apiProtocol = strings.ToLower(apiProtocol)
			}
		}

		opts.IO.LogErrorf("- glab config set -h %s git_protocol %s\n", hostname, gitProtocol)
		if err := cfg.Set(hostname, "git_protocol", gitProtocol); err != nil {
			return err
		}

		opts.IO.LogErrorf("%s Configured Git protocol.\n", c.GreenCheck())

		opts.IO.LogErrorf("- glab config set -h %s api_protocol %s\n", hostname, apiProtocol)
		if err := cfg.Set(hostname, "api_protocol", apiProtocol); err != nil {
			return err
		}

		opts.IO.LogErrorf("%s Configured API protocol.\n", c.GreenCheck())
	}
	username, err := lookupUsername(ctx, opts, hostname)
	if err != nil {
		return fmt.Errorf("error using API: %w", err)
	}

	if err := cfg.Set(hostname, "user", username); err != nil {
		return err
	}

	err = cfg.Write()
	if err != nil {
		return err
	}

	if credentialFlow.ShouldSetup() {
		err := credentialFlow.Setup(hostname, gitProtocol, username, token)
		if err != nil {
			return err
		}
	}

	opts.IO.LogErrorf("%s Logged in as %s\n", c.GreenCheck(), c.Bold(username))
	logCredentialStorage(opts.IO, keyringPref)
	opts.IO.LogErrorf("%s Configuration saved to %s\n", c.GreenCheck(), config.ConfigFile())
	opts.IO.LogErrorf("  - Host: %s\n", hostname)
	if subfolder != "" {
		opts.IO.LogErrorf("  - Subfolder: %s\n", subfolder)
	}
	if sshHostValue, _ := cfg.Get(hostname, "ssh_host"); sshHostValue != "" {
		opts.IO.LogErrorf("  - SSH host: %s\n", sshHostValue)
	}

	return nil
}

// logCredentialStorage tells the user where their credentials were stored, so
// the default keyring behavior (and any fallback to the file) is visible.
func logCredentialStorage(io *iostreams.IOStreams, keyringPref string) {
	c := io.Color()
	if keyringPref == "true" {
		io.LogErrorf("%s Stored your credentials in the operating system keyring.\n", c.GreenCheck())
	} else {
		io.LogErrorf("%s Stored your credentials in the configuration file.\n", c.GreenCheck())
	}
}

// warnEnvTokenPrecedence alerts the user when a token is provided through an
// environment variable. Such a variable takes precedence over the token stored
// in the keyring or configuration file, so glab keeps using it. It is emitted
// before the interactive prompts so it can inform the user's choice, hence the
// timing-neutral wording. The cause is diagnosed rather than assumed: the
// variable might be injected temporarily by a wrapper such as the 1Password
// shell integration (which is expected and needs no action) or persisted in
// the shell environment (which the user should remove). `type glab`
// distinguishes the two.
func warnEnvTokenPrecedence(io *iostreams.IOStreams) {
	envToken, envTokenSource := config.GetFromEnvWithSource("token")
	if envToken == "" {
		return
	}
	c := io.Color()
	io.LogErrorf("\n%s The environment variable %s is set and takes precedence over credentials stored in the keyring or configuration file.\n", c.WarnIcon(), c.Bold(envTokenSource))
	io.LogErrorf("  Run %s to find the source: an alias such as 'op plugin run -- glab' means a wrapper (for example, a 1Password shell plugin) is injecting it, which is expected and needs no action.\n", c.Bold("type glab"))
	io.LogErrorf("  A plain path means it is set in your environment (for example, a shell profile such as ~/.bashrc or ~/.zshrc, or a CI/CD variable); remove it there so glab uses your stored credentials.\n")
}

func hostnameValidator(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("hostname must be a string")
	}

	if strings.TrimSpace(s) == "" {
		return errors.New("hostname cannot be empty")
	}

	// NOTE: adding a scheme here so that `url.Parse`
	// doesn't interpret the first segment before a colon
	// as a scheme. We never expect `v` to contain
	// a scheme anyways.
	val := fmt.Sprintf("https://%s", s)
	_, err := url.Parse(val)
	if err != nil {
		return fmt.Errorf("invalid hostname: %w", err)
	}

	return nil
}

func getAccessTokenTip(hostname string) string {
	return fmt.Sprintf(`
	The minimum required scopes are 'api' and 'write_repository'.
	Generate a personal access token at https://%s/-/user_settings/personal_access_tokens/legacy/new?scopes=api,write_repository`, hostname)
}

func showTokenPrompt(ctx context.Context, io *iostreams.IOStreams, hostname string) (string, error) {
	io.LogError()
	io.LogError(heredoc.Doc(getAccessTokenTip(hostname)))

	var token string
	tokenInput := huh.NewInput().
		Title("Paste your authentication token:").
		Value(&token).
		EchoMode(huh.EchoModePassword).
		Validate(func(s string) error {
			if s == "" {
				return fmt.Errorf("required")
			}
			return nil
		})
	err := io.Run(ctx, tokenInput)
	if err != nil {
		return "", fmt.Errorf("could not prompt: %w", err)
	}

	return token, nil
}

// initialAPIHostname returns the value used to pre-fill the API-hostname
// prompt (and the value written to config when the prompt is skipped).
// Precedence: --api-host flag, then a value previously saved for the host,
// then the base hostname. Reading the saved value first prevents a
// re-authentication from silently overwriting a value the user set with
// `glab config set`.
func initialAPIHostname(cfg config.Config, hostname, apiHostFlag string) string {
	if apiHostFlag != "" {
		return apiHostFlag
	}
	if saved, _ := cfg.Get(hostname, "api_host"); saved != "" {
		return saved
	}
	return hostname
}

// initialSSHHostname returns the value used to pre-fill the SSH-hostname
// prompt. Precedence: a value previously saved for the host, then a value
// detected from local git remotes, then the base hostname.
func initialSSHHostname(cfg config.Config, hostname string) string {
	if saved, _ := cfg.Get(hostname, "ssh_host"); saved != "" {
		return saved
	}
	if detected := detectSSHHost(hostname); detected != "" {
		return detected
	}
	return hostname
}

// initialContainerRegistryDomains returns the value used to pre-fill the
// container-registry-domains prompt. Prefers a value previously saved for the
// host so a re-authentication does not silently overwrite domains the user
// set via `glab config set`; otherwise falls back to a hostname-derived
// default.
func initialContainerRegistryDomains(cfg config.Config, hostname string) string {
	if saved, _ := cfg.Get(hostname, "container_registry_domains"); saved != "" {
		return saved
	}
	return defaultContainerRegistryDomainsString(hostname)
}

// persistProtocolFlags writes --git-protocol / --api-protocol to config when
// their flags are non-empty, regardless of interactivity. Mirrors the
// --token / --job-token branches earlier in loginRun so the --web / --device
// flows persist the same flags even when stdin isn't a TTY (CI, devcontainer
// setup scripts). Values are lowercased to match how the interactive prompt
// path normalizes them.
func persistProtocolFlags(cfg config.Config, hostname, gitProtocol, apiProtocol string) error {
	if gitProtocol != "" {
		if err := cfg.Set(hostname, "git_protocol", strings.ToLower(gitProtocol)); err != nil {
			return err
		}
	}
	if apiProtocol != "" {
		if err := cfg.Set(hostname, "api_protocol", strings.ToLower(apiProtocol)); err != nil {
			return err
		}
	}
	return nil
}

func defaultContainerRegistryDomainsString(hostname string) string {
	if !strings.Contains(hostname, ":") {
		return strings.Join(
			[]string{
				hostname,
				net.JoinHostPort(hostname, "443"),
				"registry." + hostname,
			}, ",")
	}

	return strings.Join(
		[]string{
			hostname,
			"registry." + hostname,
		}, ",")
}

func setContainerRegistryDomains(cfg config.Config, hostname string, domains string) error {
	return cfg.Set(hostname, "container_registry_domains", domains)
}

// promptForSelfHostedInstance prompts the user for hostname, API hostname, and SSH hostname
// for a self-hosted GitLab instance. Returns (hostname, apiHostname, sshHostname, error).
func promptForSelfHostedInstance(ctx context.Context, opts *LoginOptions) (string, string, string, error) {
	hostname := opts.defaultHostname
	apiHostname := hostname

	// Prompt for GitLab hostname
	hostnameInput := huh.NewInput().
		Title("GitLab hostname:").
		Value(&hostname).
		Placeholder(opts.defaultHostname).
		Validate(func(s string) error {
			return hostnameValidator(s)
		})
	err := opts.IO.Run(ctx, hostnameInput)
	if err != nil {
		return "", "", "", fmt.Errorf("could not prompt: %w", err)
	}

	// Set default for API hostname
	if apiHostname == opts.defaultHostname {
		apiHostname = hostname
	}

	// Prompt for API hostname
	apiHostnameInput := huh.NewInput().
		Title("API hostname:").
		Description("For instances with a different hostname for the API endpoint.").
		Value(&apiHostname).
		Placeholder(hostname).
		Validate(func(s string) error {
			return hostnameValidator(s)
		})
	err = opts.IO.Run(ctx, apiHostnameInput)
	if err != nil {
		return "", "", "", fmt.Errorf("could not prompt: %w", err)
	}

	// Prompt for SSH hostname
	sshHostname := hostname
	// Try to detect from git remotes as a suggestion
	if detectedSSH := detectSSHHost(hostname); detectedSSH != "" {
		sshHostname = detectedSSH
	}

	sshHostnameInput := huh.NewInput().
		Title("SSH hostname:").
		Description("For instances with a different hostname for SSH git operations.").
		Value(&sshHostname).
		Placeholder(hostname).
		Validate(func(s string) error {
			return hostnameValidator(s)
		})
	err = opts.IO.Run(ctx, sshHostnameInput)
	if err != nil {
		return "", "", "", fmt.Errorf("could not prompt: %w", err)
	}

	return hostname, apiHostname, sshHostname, nil
}

// splitHostnameAndSubfolder splits a hostname that may contain a subfolder path.
// Examples:
//   - "example.com" → ("example.com", "")
//   - "example.com/gitlab" → ("example.com", "gitlab")
//   - "example.com:3000/gitlab" → ("example.com:3000", "gitlab")
//   - "https://example.com/gitlab" → ("example.com", "gitlab")
func splitHostnameAndSubfolder(input string) (string, string) {
	// Ensure the input has a scheme for proper URL parsing
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Parse the URL
	u, err := url.Parse(input)
	if err != nil {
		// Fallback to string manipulation if parsing fails
		input = strings.TrimPrefix(input, "https://")
		input = strings.TrimPrefix(input, "http://")
		input = strings.TrimSuffix(input, "/")
		return glinstance.ExtractSubfolder(input)
	}

	// Use u.Host to preserve port information (e.g., "example.com:3000")
	hostname := u.Host
	subfolder := strings.Trim(u.Path, "/")

	return hostname, subfolder
}

// detectSSHHost attempts to detect the SSH hostname from git remotes.
// Returns empty string if not found or same as HTTP hostname.
func detectSSHHost(httpHostname string) string {
	// Check if we're in a git repository
	remotes, err := git.Remotes()
	if err != nil {
		return ""
	}

	// Look for SSH remotes and extract hostname
	// Note: git.ParseURL() already normalizes SCP-style URLs (git@host:path)
	// to SSH URLs (ssh://host/path) when remotes are loaded, so the Scheme=="ssh"
	// check below catches all SSH remotes regardless of original URL format.
	for _, remote := range remotes {
		if remote.FetchURL != nil && remote.FetchURL.Scheme == "ssh" {
			sshHost := remote.FetchURL.Hostname()
			if sshHost != "" && sshHost != httpHostname {
				return sshHost
			}
		}
	}

	return ""
}
