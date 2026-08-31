package config

import (
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Scope int

const (
	ScopeGlobal Scope = iota
	ScopePerHost
)

type ValueType int

const (
	TypeString ValueType = iota
	TypeBool
	TypeList
)

// KeyDef is the single source of truth for a config key. It drives
// NewBlankConfig, KnownKeys, defaultFor, ConfigKeyEquivalence,
// EnvKeyEquivalence, and keyring eligibility.
type KeyDef struct {
	Name  string
	Scope Scope
	Type  ValueType
	// Default is the YAML scalar seed value. For TypeList it's ignored
	// and an empty sequence is emitted instead.
	Default     string
	Description string
	// EnvVars overrides the default env-var (uppercase Name). CI-autologin
	// overrides are layered on top by EnvKeyEquivalence.
	EnvVars []string
	Aliases []string
	// UserSettable false keeps the key off `config set` and out of the
	// blank config, but Get and env-var lookups still resolve it.
	UserSettable bool
	// HelpHidden keeps a key the CLI maintains for itself out of generated
	// help. It stays settable.
	HelpHidden bool
	Keyring    bool
	// Fallback makes Default apply to runtime Get calls when the key is
	// missing from the config, not just to the blank-config seed.
	Fallback bool
}

// KeySchema is the declared list of every config key glab understands.
// Order is preserved when building the blank config.
var KeySchema = []KeyDef{
	// ---------------- Global ----------------
	{
		Name: "git_protocol", Scope: ScopeGlobal, Type: TypeString,
		Default: "ssh", UserSettable: true, Fallback: true,
		Description: "What protocol to use when performing Git operations. Supported values: 'ssh', 'https'.",
	},
	{
		Name: "branch_prefix", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "Prefix used by 'glab stack' when naming generated branches. Defaults to the\ncurrent user's username (from 'os/user.Current'), falling back to 'glab-stack' if unavailable.",
	},
	{
		Name: "remote_alias", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Aliases:      []string{"git_remote_url_var", "git_remote_alias", "remote_nickname", "git_remote_nickname"},
		EnvVars:      []string{"GIT_REMOTE_URL_VAR", "GIT_REMOTE_ALIAS", "REMOTE_ALIAS", "REMOTE_NICKNAME", "GIT_REMOTE_NICKNAME"},
		Description:  "Name of the 'git remote' that points at the GitLab repository. Used to\nresolve which remote to operate against when multiple are configured.",
	},
	{
		Name: "editor", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Aliases:      []string{"visual", "glab_editor"},
		EnvVars:      []string{"GLAB_EDITOR", "VISUAL", "EDITOR"},
		Description:  "What editor glab should run when creating issues, merge requests, etc. This global config cannot be overridden by hostname.",
	},
	{
		Name: "browser", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "What browser glab should run when opening links. This global config cannot be overridden by hostname.",
	},
	{
		Name: "glab_pager", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "Your desired pager command to use, such as 'less -R'. Takes precedence over the PAGER environment variable. GLAB_PAGER takes precedence over both.",
	},
	{
		Name: "debug", Scope: ScopeGlobal, Type: TypeBool,
		Default: "false", UserSettable: true,
		EnvVars:     []string{"GLAB_DEBUG"},
		Description: "Output more logging information, including underlying Git commands, expanded aliases, and DNS error details.",
	},
	{
		Name: "glamour_style", Scope: ScopeGlobal, Type: TypeString,
		Default: "dark", UserSettable: true, Fallback: true,
		Description: "Set your desired Markdown renderer style. Available options are [dark, light, notty]. To set a custom style, refer to https://github.com/charmbracelet/glamour#styles",
	},
	{
		Name: "check_update", Scope: ScopeGlobal, Type: TypeBool,
		Default: "true", UserSettable: true,
		Description: "Allow glab to automatically check for updates and notify you when there are new updates.",
	},
	{
		Name: "last_update_check_timestamp", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "Last update check timestamp, used for checking when the last update check was performed.",
	},
	{
		Name: "show_whats_new", Scope: ScopeGlobal, Type: TypeBool,
		Default: "true", UserSettable: true,
		EnvVars:     []string{"GLAB_SHOW_WHATS_NEW"},
		Description: "Show a one-time post-upgrade banner pointing at 'glab whatsnew' when a new version is detected.",
	},
	{
		Name: "last_seen_version", Scope: ScopeGlobal, Type: TypeString,
		// Seeded so the post-upgrade banner can surface immediately when
		// existing users upgrade to the release that ships `glab whatsnew`.
		// Bump only when intentionally re-announcing the feature.
		Default: "v1.100.0", UserSettable: true, Fallback: true, HelpHidden: true,
		Description: "Last glab version a post-upgrade banner was shown for (automatically set).",
	},
	{
		Name: "last_whatsnew_version", Scope: ScopeGlobal, Type: TypeString,
		// Same seeded default as last_seen_version so the default whatsnew
		// invocation has a non-empty baseline before the user runs it.
		Default: "v1.100.0", UserSettable: true, Fallback: true, HelpHidden: true,
		Description: "Last glab version 'glab whatsnew' rendered notes for (automatically set).",
	},
	{
		Name: "notify_skill_updates", Scope: ScopeGlobal, Type: TypeBool,
		Default: "true", UserSettable: true,
		EnvVars:     []string{"GLAB_NOTIFY_SKILL_UPDATES"},
		Description: "Show a notice when an installed agent skill (bundled or remote) has updates available.",
	},
	{
		Name: "display_hyperlinks", Scope: ScopeGlobal, Type: TypeBool,
		Default: "true", UserSettable: true,
		Description: "Whether or not to display hyperlinks in terminal output. Defaults to true (enabled for TTYs). Set to false to disable. Force hyperlinks in non-TTY environments by setting FORCE_HYPERLINKS=1.",
	},
	{
		Name: "host", Scope: ScopeGlobal, Type: TypeString,
		Default: "gitlab.com", UserSettable: true,
		Aliases:     []string{"gitlab_host", "gitlab_uri", "gl_host"},
		EnvVars:     []string{"GITLAB_HOST", "GITLAB_URI", "GL_HOST"},
		Description: "Default GitLab hostname to use.",
	},
	{
		Name: "no_prompt", Scope: ScopeGlobal, Type: TypeBool,
		Default: "false", UserSettable: true,
		Aliases:     []string{"prompt_disabled"},
		EnvVars:     []string{"NO_PROMPT", "PROMPT_DISABLED"},
		Description: "Set to true (1) to disable prompts, or false (0) to enable them.",
	},
	{
		Name: "telemetry", Scope: ScopeGlobal, Type: TypeBool,
		Default: "true", UserSettable: true,
		EnvVars:     []string{"GLAB_SEND_TELEMETRY"},
		Description: "Set to false (0) to disable sending usage data to your GitLab instance or true (1) to enable.\nSee https://docs.gitlab.com/administration/settings/usage_statistics/\nfor more information",
	},
	// ---- Duo CLI binarymgr keys ----
	{
		Name: "duo_cli_auto_run", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "Automatically run GitLab Duo CLI without prompting (true/false). Set to true to skip the confirmation prompt.",
	},
	{
		Name: "duo_cli_auto_download", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "Automatically download Duo CLI binary without prompting (true/false).",
	},
	{
		Name: "duo_cli_binary_path", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		EnvVars:     []string{"GLAB_DUO_CLI_BINARY_PATH"},
		Description: "Path to the installed Duo CLI binary (automatically set). Default: ~/.config/glab-cli/bin/duo",
	},
	{
		Name: "duo_cli_binary_version", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "Version of the installed Duo CLI binary (automatically set).",
	},
	{
		Name: "duo_cli_binary_checksum", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "SHA256 checksum of the installed Duo CLI binary (automatically set).",
	},
	{
		Name: "duo_cli_last_update_check", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "Last time an update check was performed (automatically set).",
	},
	// ---- Orbit local binarymgr keys ----
	{
		Name: "orbit_local_auto_run", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "Automatically run Orbit local CLI without prompting (true/false). Set to true to skip the confirmation prompt.",
	},
	{
		Name: "orbit_local_auto_download", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true,
		Description:  "Automatically download Orbit local CLI binary without prompting (true/false).",
	},
	{
		Name: "orbit_local_binary_path", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		EnvVars:     []string{"GLAB_ORBIT_LOCAL_BINARY_PATH"},
		Description: "Path to the installed Orbit local CLI binary (automatically set). Default: ~/.config/glab-cli/bin/orbit",
	},
	{
		Name: "orbit_local_binary_version", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "Version of the installed Orbit local CLI binary (automatically set).",
	},
	{
		Name: "orbit_local_binary_checksum", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "SHA256 checksum of the installed Orbit local CLI binary (automatically set).",
	},
	{
		Name: "orbit_local_last_update_check", Scope: ScopeGlobal, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		Description: "Last time an Orbit local CLI update check was performed (automatically set).",
	},

	// ---------------- Per-host ----------------
	{
		Name: "api_protocol", Scope: ScopePerHost, Type: TypeString,
		Default: "https", UserSettable: true, Fallback: true,
		Description: "What protocol to use to access the API endpoint. Supported values: 'http', 'https'.",
	},
	{
		Name: "api_host", Scope: ScopePerHost, Type: TypeString,
		Default: "gitlab.com", UserSettable: true,
		Aliases:     []string{"gitlab_api_host"},
		EnvVars:     []string{"GITLAB_API_HOST"},
		Description: "Configure host for API endpoint. Defaults to the host itself.",
	},
	{
		Name: "subfolder", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Aliases:      []string{"gitlab_subfolder"},
		EnvVars:      []string{"GITLAB_SUBFOLDER"},
		Description:  "Subfolder where GitLab is installed (e.g., 'gitlab' for https://example.com/gitlab/).\nUse this when GitLab is hosted at a subfolder rather than domain root.\nSupports nested paths (e.g., 'apps/gitlab' for https://example.com/apps/gitlab/).\nSlashes are automatically trimmed, so 'gitlab', '/gitlab', and 'gitlab/' are equivalent.\nOnly applies to HTTP/HTTPS operations (API and Git clone).",
	},
	{
		Name: "ssh_host", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Aliases:      []string{"gitlab_ssh_host"},
		EnvVars:      []string{"GITLAB_SSH_HOST"},
		Description:  "Alternate hostname for SSH Git operations (e.g., 'ssh.example.com' or 'git.example.com').\nUse this when SSH uses a different hostname than HTTP/API operations.\nOnly affects SSH cloning and Git operations.",
	},
	{
		Name: "token", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true, Keyring: true,
		Aliases:     []string{"gitlab_token", "oauth_token"},
		EnvVars:     []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN"},
		Description: "Your GitLab access token. To get one, read https://docs.gitlab.com/user/profile/personal_access_tokens/",
	},
	{
		Name: "job_token", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true, Keyring: true,
		Description: "CI job token used for Job-Token authentication. Typically populated\nautomatically from CI_JOB_TOKEN when CI auto-login is enabled.",
	},
	{
		Name: "user", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true, HelpHidden: true,
		EnvVars:     []string{"GLAB_USER"},
		Description: "Username associated with the configured token. Set automatically on login.",
	},
	{
		Name: "client_id", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		EnvVars:      []string{"GITLAB_CLIENT_ID"},
		Description:  "OAuth application client ID. Required when authenticating with OAuth\nagainst a self-managed GitLab instance.",
	},
	{
		Name: "use_keyring", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description:  "Store the host's credentials in the operating system's keyring (true/false).\nSet automatically by 'glab auth login', which defaults to 'true' when a keyring\nbackend is available. Empty is treated as false (plaintext file storage).",
	},
	{
		Name: "proxy", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description:  "Custom proxy for this host. Overrides environment proxy settings when set.",
	},
	{
		Name: "ca_cert", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description:  "Path to a CA certificate (PEM) used to verify the GitLab server's\nTLS certificate. Useful for self-signed or private certificate authorities.",
	},
	{
		Name: "client_cert", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description:  "Path to a client certificate (PEM) used for mutual TLS authentication.",
	},
	{
		Name: "client_key", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description:  "Path to the private key (PEM) that matches client_cert.",
	},
	{
		Name: "skip_tls_verify", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description:  "Skip TLS certificate verification when talking to this host (true/false).\nEmpty is treated as false. Use only for development; do not enable in production.",
	},
	{
		Name: "container_registry_domains", Scope: ScopePerHost, Type: TypeString,
		Default: "gitlab.com,gitlab.com:443,registry.gitlab.com", UserSettable: true,
		Description: "The domains of associated container registries. These are used to configure the\nDocker credential helper.",
	},
	{
		Name: "artifact_registry_domains", Scope: ScopePerHost, Type: TypeString,
		UserSettable: true,
		Description: "The domains of associated Artifact Registries. These are used to configure the\n" +
			"Docker credential helper. Only list a domain here if it is actually backed by\n" +
			"GitLab Artifact Registry: the credential helper tries this key first, and a\n" +
			"successful token exchange is used as-is, with no fallback to\n" +
			"container_registry_domains. A container-registry domain listed here by mistake\n" +
			"gets an artifact-registry token the registry rejects, and 'docker pull' hard-fails.",
	},
	{
		Name: "custom_headers", Scope: ScopePerHost, Type: TypeList,
		UserSettable: true,
		Description:  "Custom HTTP headers to add to all HTTP requests made by glab. Each header must use exactly one of value, valueFromEnv, or valueFromCommand. A command must print the complete header value on one line. glab runs it once for each process.\n- name: Proxy-Authorization\n  valueFromCommand: token-helper --audience example\n- name: Cf-Access-Client-Secret\n  valueFromEnv: MY_SECRET_VALUE",
	},

	// ---------------- CLI-managed internal state ----------------
	// These keys are written by the CLI during auth/login/refresh flows.
	// They are NOT user-settable (config set rejects them), are NOT
	// included in the blank config, but the runtime config reader still
	// resolves them via env vars in the normal way.
	{
		Name: "is_oauth2", Scope: ScopePerHost, Type: TypeString,
		UserSettable: false,
		EnvVars:      []string{"GLAB_IS_OAUTH2"},
		Description:  "CLI-managed flag indicating the host was authenticated via OAuth.",
	},
	{
		Name: "oauth2_refresh_token", Scope: ScopePerHost, Type: TypeString,
		UserSettable: false, Keyring: true,
		Description: "CLI-managed OAuth refresh token. Written by 'glab auth login'.",
	},
	{
		Name: "oauth2_expiry_date", Scope: ScopePerHost, Type: TypeString,
		UserSettable: false,
		Description:  "CLI-managed OAuth token expiry timestamp.",
	},
	{
		Name: "refresh_token", Scope: ScopePerHost, Type: TypeString,
		UserSettable: false,
		Description:  "CLI-managed refresh token; superseded by oauth2_refresh_token.",
	},
}

func findKeyDef(key string) *KeyDef {
	for i := range KeySchema {
		if KeySchema[i].Name == key {
			return &KeySchema[i]
		}
	}
	return nil
}

func resolveAlias(key string) string {
	key = strings.ToLower(key)
	for i := range KeySchema {
		if KeySchema[i].Name == key {
			return key
		}
		if slices.Contains(KeySchema[i].Aliases, key) {
			return KeySchema[i].Name
		}
	}
	return key
}

var defaultAliases = []struct{ Name, Command string }{
	{"ci", "pipeline ci"},
	{"co", "mr checkout"},
}

const defaultBlankHost = "gitlab.com"

func userSettableKeys(scope Scope) []*KeyDef {
	var out []*KeyDef
	for i := range KeySchema {
		kd := &KeySchema[i]
		if kd.UserSettable && kd.Scope == scope {
			out = append(out, kd)
		}
	}
	return out
}

func rootConfig() *yaml.Node {
	top := &yaml.Node{Kind: yaml.MappingNode}

	for _, kd := range userSettableKeys(ScopeGlobal) {
		top.Content = append(top.Content, keyNode(kd), valueNode(kd))
	}

	// hosts:
	hostsKey := &yaml.Node{
		Kind:        yaml.ScalarNode,
		Value:       "hosts",
		HeadComment: "# Configuration specific for GitLab instances.",
	}
	hostsMap := &yaml.Node{Kind: yaml.MappingNode}
	hostName := &yaml.Node{Kind: yaml.ScalarNode, Value: defaultBlankHost}
	hostBody := &yaml.Node{Kind: yaml.MappingNode}
	for _, kd := range userSettableKeys(ScopePerHost) {
		hostBody.Content = append(hostBody.Content, keyNode(kd), valueNode(kd))
	}
	hostsMap.Content = []*yaml.Node{hostName, hostBody}
	top.Content = append(top.Content, hostsKey, hostsMap)

	// aliases:
	aliasesKey := &yaml.Node{
		Kind:        yaml.ScalarNode,
		Value:       "aliases",
		HeadComment: "# Use aliases to create nicknames for glab commands. Supports shell-executable aliases that may not be glab commands.",
	}
	aliasesMap := &yaml.Node{Kind: yaml.MappingNode}
	for _, a := range defaultAliases {
		aliasesMap.Content = append(aliasesMap.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: a.Name},
			&yaml.Node{Kind: yaml.ScalarNode, Value: a.Command},
		)
	}
	top.Content = append(top.Content, aliasesKey, aliasesMap)

	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{top}}
}

func keyNode(kd *KeyDef) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: kd.Name}
	if kd.Description != "" {
		n.HeadComment = "# " + strings.ReplaceAll(kd.Description, "\n", "\n# ")
	}
	return n
}

func valueNode(kd *KeyDef) *yaml.Node {
	if kd.Type == TypeList {
		return &yaml.Node{Kind: yaml.SequenceNode}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Value: kd.Default}
}
