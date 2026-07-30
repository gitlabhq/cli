package cmdutils

import (
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// outputFormatValue also records the format on IOStreams. cobra sets flags
// before argument validation and RunE, so the format is known even when one of
// them fails — which is what lets the error handler in cmd/glab/main.go report
// that failure as JSON.
type outputFormatValue struct {
	*enumValue[string]
	io *iostreams.IOStreams
}

func (v *outputFormatValue) Set(format string) error {
	if err := v.enumValue.Set(format); err != nil {
		return err
	}
	v.io.SetOutputFormat(format)
	return nil
}

// EnableJSONOutput adds the --output/-F flag to a command for JSON output
// support, and also registers --jq via AddJQFlag so callers do not have to
// invoke both helpers.
//
// By default it uses a standard description. Pass a custom description to
// override.
func EnableJSONOutput(cmd *cobra.Command, io *iostreams.IOStreams, outputFormat *string, customDescription ...string) {
	description := "Format output as: text, json."
	if len(customDescription) > 0 && customDescription[0] != "" {
		description = customDescription[0]
	}

	cmd.Flags().VarP(
		&outputFormatValue{
			enumValue: NewEnumValue([]string{"text", "json"}, "text", outputFormat),
			io:        io,
		},
		"output",
		"F",
		description,
	)
	AddJQFlag(cmd, io)
}
