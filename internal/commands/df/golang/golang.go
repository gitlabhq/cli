package golang

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/df/dfcmd"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return dfcmd.NewProxyCmd(f, dfcmd.ProxySpec{
		Manager: pm.Golang,
		Use:     "go <go args>",
		Short:   "Run go through the GitLab Dependency Firewall.",
		Long: heredoc.Docf(`
			Run the go binary through the GitLab Dependency Firewall. The command checks each module download against the policy for the current project, refuses blocked modules, and summarizes the results after the run.

			Module downloads go through a Go module proxy that the command starts on %[1]s127.0.0.1%[1]s for the run. No certificate is involved, so your system trust store is not changed.

			For the run, %[1]sGOPROXY%[1]s points at that proxy, %[1]sGONOPROXY%[1]s is %[1]snone%[1]s, and %[1]sGO111MODULE%[1]s is %[1]son%[1]s, so every module download is checked. Private modules matched by %[1]sGOPRIVATE%[1]s are also checked, then fetched the way your configuration fetches them.

			If you set %[1]sGOPROXY=off%[1]s, the command keeps it. If you set %[1]sGOPROXY%[1]s to any other non-default value, the command stops before it runs %[1]sgo%[1]s.

			The command checks only modules that %[1]sgo%[1]s downloads during the run:

			- Modules already in your module cache are not checked. In CI, point %[1]sGOMODCACHE%[1]s at an empty directory to check every module.
			- Vendored code and %[1]sreplace%[1]s directives that point at a local path are not checked.
			- Go toolchain downloads are not checked by policy. %[1]sgo%[1]s verifies them against the checksum database.

			Module paths and versions, including private ones, are sent to the policy API and written to %[1]s.gitlab/df/ci-log.json%[1]s. CI jobs that upload that file publish them with the job's artifacts.

			Tests that make HTTPS requests can fail certificate checks under this command. Run %[1]sgo test%[1]s outside the firewall if your tests reach the network.

			All arguments are forwarded to %[1]sgo%[1]s verbatim.
		`, "`"),
		Example: heredoc.Doc(`
			# Download modules through the Dependency Firewall
			glab dependency-firewall go mod download

			# Add a dependency, checking it against the policy first
			glab dependency-firewall go get github.com/pkg/errors@v0.9.1
		`),
	})
}
