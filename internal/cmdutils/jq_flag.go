package cmdutils

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// AddJQFlag registers the --jq flag on a command that emits JSON via
// IOStreams.PrintJSON. The flag's value is parsed (and the resulting jq
// query compiled) by cobra at parse time, via the IOStreams.JQFilter
// pflag.Value attached to io.JQ.
//
// In PreRun, the helper verifies that the command's output flag (looked
// up by name: --output first, then --output-format) is set to "json".
// Commands without either output flag always emit JSON and skip this
// check. The verification fails the command before RunE
// runs, so non-JSON output is never produced when --jq is active.
func AddJQFlag(cmd *cobra.Command, io *iostreams.IOStreams) {
	if io.JQ == nil {
		io.JQ = &iostreams.JQFilter{}
	}
	cmd.Flags().Var(io.JQ, "jq", "Filter JSON output with a jq expression.")

	origPreRunE := cmd.PreRunE
	origPreRun := cmd.PreRun
	cmd.PreRun = nil
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if io.JQ.IsActive() {
			if outFlag := lookupOutputFlag(c); outFlag != nil && outFlag.Value.String() != "json" {
				return &FlagError{Err: fmt.Errorf("using --jq requires --%s=json (got %q)", outFlag.Name, outFlag.Value.String())}
			}
		}

		if origPreRunE != nil {
			return origPreRunE(c, args)
		}
		if origPreRun != nil {
			origPreRun(c, args)
		}
		return nil
	}
}

func lookupOutputFlag(c *cobra.Command) *pflag.Flag {
	if f := c.Flag("output"); f != nil {
		return f
	}
	return c.Flag("output-format")
}
