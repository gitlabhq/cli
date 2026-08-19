package cli

import (
	"context"
	"errors"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"golang.org/x/sys/cpu"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// duoMaxCompatibleMajorVersion caps Duo CLI auto-updates to this major
// version. Bump after validating compatibility against the new major.
const duoMaxCompatibleMajorVersion = 9

// Spec returns the binarymgr.Spec describing the Duo CLI binary. It is
// exported so tests and other consumers can introspect it.
func Spec() binarymgr.Spec {
	return binarymgr.Spec{
		DisplayName:        "GitLab Duo CLI",
		ProjectID:          "46519181",
		PackageName:        "duo-cli",
		ConfigPrefix:       "duo_cli",
		EnvVarPrefix:       "GLAB_DUO_CLI",
		MaxCompatibleMajor: duoMaxCompatibleMajorVersion,
		SupportedOS:        []string{"darwin", "linux", "windows"},
		NormalizeArch:      duoNormalizeArch,
		AssetName:          duoAssetName,
		InstalledName:      duoInstalledName,
		// Duo ships raw binaries — no extraction needed.
		Extract: nil,
	}
}

func duoNormalizeArch(goos, goarch string) (string, error) {
	return duoNormalizeArchFor(goos, goarch, detectLinuxX64ArchVariant)
}

// duoNormalizeArchFor is duoNormalizeArch's testable core. The Linux x64
// variant detector is injected so tests don't depend on the host CPU.
func duoNormalizeArchFor(goos, goarch string, linuxX64Variant func() string) (string, error) {
	switch goarch {
	case "amd64":
		switch goos {
		case "windows":
			return "x64-baseline", nil
		case "linux":
			return linuxX64Variant(), nil
		}
		return "x64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	}
	return "", binarymgr.ErrUnsupportedPlatform
}

// detectLinuxX64ArchVariant mirrors the upstream Duo CLI installer's
// detect_linux_x64_variant: "x64-modern" when the host CPU advertises AVX2,
// otherwise "x64" (baseline).
//
// AVX2 is a sufficient proxy for the full feature set Bun's modern target
// requires (AVX2 + BMI2 + FMA): every CPU that advertises AVX2 also has
// BMI2 and FMA (Haswell+ / Excavator+), so checking AVX2 alone matches the
// upstream installer's bash detect_linux_x64_variant exactly.
func detectLinuxX64ArchVariant() string {
	// Linux uses "x64" instead of "x64-baseline" to preserve
	// compatibility with previous versions of glab as x64 should work on most systems
	// and it matches the duo cli install script:
	// https://gitlab.com/gitlab-org/editor-extensions/gitlab-lsp/-/blob/247ec22ab64e4160ff5776c4254598623f537738/packages/cli/scripts/install_duo_cli.sh
	if cpu.X86.HasAVX2 {
		return "x64-modern"
	}
	return "x64"
}

func duoAssetName(goos, arch string) string {
	name := "duo-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func duoInstalledName(goos string) string {
	if goos == "windows" {
		return "duo.exe"
	}
	return "duo"
}

// AppendBinaryStatusFooter registers a help function on cmd that renders glab's
// standard help and then appends a section describing how to reach the GitLab
// Duo CLI's own help, depending on whether the binary is installed.
func AppendBinaryStatusFooter(cmd *cobra.Command, f cmdutils.Factory) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		c.Root().HelpFunc()(c, args)

		io := f.IO()
		io.LogInfo(io.Color().Bold("GITLAB DUO CLI"))

		path, version, installed := binaryStatus(f.Config())
		if installed {
			io.LogInfo(utils.Indent(heredoc.Docf(`
			  Installed: %s (%s)
			  Run 'glab duo cli help' for the GitLab Duo CLI commands and flags.`, version, path), "  "))
			return
		}

		io.LogInfo(utils.Indent(heredoc.Doc(`
		  The GitLab Duo CLI binary is not installed yet. To get started:

		    1. glab auth login          # authenticate, once
		    2. glab duo cli --install   # download the GitLab Duo CLI binary
		    3. glab duo cli             # start an interactive session

		  Or run 'glab duo cli' and follow the prompts.
		  After it is installed, 'glab duo cli help' shows the GitLab Duo CLI
		  commands and flags.`), "  "))
	})
}

// binaryStatus resolves the Duo CLI binary that would be used, and reports
// whether it is present on disk. Path resolution mirrors binarymgr.Runner.
// Errors are logged via dbg (not returned) so a real failure is diagnosable
// under GLAB_DEBUG instead of silently rendering as "not installed".
func binaryStatus(cfg config.Config) (string, string, bool) {
	path, err := cfg.Get("", "duo_cli_binary_path")
	if err != nil {
		dbg.Debugf("binaryStatus: reading duo_cli_binary_path from config: %v", err)
	}
	if path == "" {
		var mbpErr error
		path, mbpErr = binarymgr.ManagedBinaryPath(Spec())
		if mbpErr != nil {
			dbg.Debugf("binaryStatus: resolving managed Duo CLI binary path: %v", mbpErr)
		}
	}
	version, err := cfg.Get("", "duo_cli_binary_version")
	if err != nil {
		dbg.Debugf("binaryStatus: reading duo_cli_binary_version from config: %v", err)
	}
	if version == "" {
		version = "unknown version"
	}
	if _, err := os.Stat(path); err != nil {
		return path, version, false
	}
	return path, version, true
}

// NewCmd creates the `glab duo cli` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	spec := Spec()
	cmd := &cobra.Command{
		Use:   "cli [command]",
		Short: "Run the GitLab Duo CLI.",
		Long: heredoc.Docf(`
			Run the GitLab Duo CLI (%[1]sduo%[1]s) through %[1]sglab%[1]s.

			The GitLab Duo CLI brings the GitLab Duo Agent Platform to your terminal. Ask GitLab Duo questions about your codebase and use it to autonomously perform actions on your behalf.

			The GitLab Duo CLI runs in two modes:

			- Interactive (default): %[1]sglab duo cli%[1]s opens a session for multiple prompts, with build and plan modes.
			- Headless: %[1]sglab duo cli run --goal "<prompt>"%[1]s runs a single prompt and exits. For use in runners, scripts, and automated workflows.

			When you use the GitLab Duo CLI through %[1]sglab%[1]s, %[1]sglab%[1]s handles authentication for you. You authenticate only once.

			Prerequisites:

			- Use GitLab 19.2 or later.
			- Run %[1]sglab auth login%[1]s to authenticate.
			- Meet the [prerequisites for GitLab Duo Agent Platform](https://docs.gitlab.com/user/duo_agent_platform/#prerequisites).
			- Set a [default GitLab Duo namespace](https://docs.gitlab.com/user/profile/preferences/#namespace-resolution-in-your-local-environment), or run the command in a project that has GitLab Duo access.
			- For GitLab Self-Managed and GitLab Dedicated on 19.2 or later, turn on [GitLab Duo CLI access](https://docs.gitlab.com/user/gitlab_duo_cli/#turn-gitlab-duo-cli-access-on-or-off). It is on by default.

			If you are on GitLab 18.11 to 19.1, you can use the GitLab Duo CLI by turning on [beta and experimental features](https://docs.gitlab.com/user/duo_agent_platform/turn_on_off/#turn-on-beta-and-experimental-features).

			Configuration options:

			- %[1]sduo_cli_auto_run%[1]s: Skip the run confirmation prompt.
			- %[1]sduo_cli_auto_download%[1]s: Skip the download confirmation prompt.

			%[1]sglab%[1]s passes all other arguments and flags through to the GitLab Duo CLI binary. To see the GitLab Duo CLI commands and flags, run %[1]sglab duo cli help%[1]s.

			For more information, see the [GitLab Duo CLI documentation](https://docs.gitlab.com/user/gitlab_duo_cli/).
		`, "`"),
		Annotations: map[string]string{
			"help:environment": heredoc.Docf(`
				- %[1]sGLAB_DUO_CLI_BINARY_PATH%[1]s: Use a local binary instead of the managed one.
				  Skips download, version checks, and updates. Can also be set through the
				  %[1]sduo_cli_binary_path%[1]s configuration key.
				`, "`"),
		},
		Example: heredoc.Doc(`
			# Start an interactive GitLab Duo CLI session
			glab duo cli

			# Use the GitLab Duo CLI in headless mode with a single prompt
			glab duo cli run --goal "Fix the failing tests in this project"

			# Pass any command or flag through to the GitLab Duo CLI binary (for example: model, version, run)
			glab duo cli <command>
			glab duo cli --model claude_sonnet_4_6

			# Show GitLab CLI help content
			glab duo cli --help

			# Show GitLab Duo CLI help content, including commands and flags
			glab duo cli help

			# Run without prompts (for use in scripts and non-interactive environments)
			glab duo cli --yes

			# Install the GitLab Duo CLI binary
			glab duo cli --install

			# Install the GitLab Duo CLI binary without prompts
			glab duo cli --install --yes

			# Check for and install updates
			glab duo cli --update`),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := newRunner(f.IO(), f.Config(), spec)

			// DisableFlagParsing is on, so split glab-owned flags from
			// pass-through args manually. --help/-h shows glab's help only
			// when nothing has been collected for pass-through yet.
			var remaining []string
			for _, arg := range args {
				switch arg {
				case "--update":
					runner.Update = true
				case "--install":
					runner.Install = true
				case "--yes", "-y":
					runner.Yes = true
				case "--help", "-h":
					if len(remaining) == 0 {
						return cmd.Help()
					}
					remaining = append(remaining, arg)
				default:
					remaining = append(remaining, arg)
				}
			}
			runner.Args = remaining

			if runner.Install && runner.Update {
				return errors.New("the --install and --update flags are mutually exclusive")
			}

			warnIfSnapConfined(f.IO(), os.Getenv, runner.Install, runner.Update)

			if runner.Install {
				return runner.HandleInstall(cmd.Context())
			}
			return runner.Run(cmd.Context())
		},
	}

	// Registered for documentation only — DisableFlagParsing means Cobra
	// never parses these; the RunE switch above handles them manually.
	fl := cmd.Flags()
	fl.BoolP("yes", "y", false, "Skip confirmation prompts.")
	fl.Bool("install", false, "Install the GitLab Duo CLI binary without running it.")
	fl.Bool("update", false, "Check for and install updates to the binary.")

	AppendBinaryStatusFooter(cmd, f)

	return cmd
}

// warnIfSnapConfined prints a clear warning when glab is running inside a
// snap. The Duo CLI authenticates by spawning `glab auth credential-helper`,
// but snap's AppArmor profile blocks the downloaded Duo binary from exec'ing
// back into glab, so the credential lookup fails with a confusing
// "no credentials found" error even after a successful `glab auth login`.
//
// The warning only applies to the actual `glab duo cli` run path: --install
// and --update don't depend on the credential-helper callback and stay
// silent. getenv is injected for testability.
func warnIfSnapConfined(io *iostreams.IOStreams, getenv func(string) string, install, update bool) {
	if install || update {
		return
	}
	if getenv("SNAP") == "" {
		return
	}
	msg := heredoc.Docf(`
		%[1]s glab is running under snap confinement.

		The GitLab Duo CLI authenticates by spawning %[2]sglab auth credential-helper%[2]s, but
		snap's sandbox blocks that callback. Authentication is likely to fail with
		"no credentials found" even after a successful %[2]sglab auth login%[2]s.

		To use %[2]sglab duo cli%[2]s, install glab from a non-snap source:
		  - Homebrew:     %[2]sbrew install glab%[2]s
		  - mise:         %[2]smise use -g glab@latest%[2]s
		  - Native binary: https://gitlab.com/gitlab-org/cli/-/releases

		Or set the GITLAB_TOKEN environment variable (%[2]sGITLAB_TOKEN=glpat-xyz glab duo cli%[2]s).
		For GitLab Self-Managed, also set GITLAB_URL (%[2]sGITLAB_URL=https://gitlab.example.com GITLAB_TOKEN=glpat-xyz glab duo cli%[2]s).

	`, io.Color().DotWarnIcon(), "`")
	io.LogError(msg)
}

// newRunner builds a binarymgr.Runner for the Duo CLI. The Executor is
// platform-specific: syscall.Exec on Unix (in execute_unix.go), subprocess
// on Windows (in execute_windows.go). Each declares executeDuoCLI(io, ...).
func newRunner(io *iostreams.IOStreams, cfg config.Config, spec binarymgr.Spec) *binarymgr.Runner {
	return &binarymgr.Runner{
		IO:      io,
		Cfg:     cfg,
		Spec:    spec,
		Manager: binarymgr.NewManager(io, spec),
		Executor: func(ctx context.Context, binaryPath string, args []string) error {
			return executeDuoCLI(ctx, io, binaryPath, args)
		},
		UpdateCommand: "duo cli",
	}
}
