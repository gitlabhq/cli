package cmdutils

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	descriptionFlag     = "description"
	descriptionFileFlag = "description-file"
)

// AddDescriptionFileFlag registers --description-file on cmd and marks it mutually
// exclusive with --description. The --description flag must already be registered.
//
// resource names the thing being described, for example "issue" or "merge request".
func AddDescriptionFileFlag(cmd *cobra.Command, resource string) {
	cmd.Flags().String(descriptionFileFlag, "",
		fmt.Sprintf("Read the %s description from a file. Use \"-\" to read from standard input.", resource))
	cmd.MarkFlagsMutuallyExclusive(descriptionFlag, descriptionFileFlag)
}

// ResolveDescriptionFile reads the file named by --description-file into the
// --description flag, so commands can keep reading the description from
// --description alone. It is a no-op when --description-file was not passed.
//
// A path of "-" reads from standard input. Because the two flags are mutually
// exclusive, this never overwrites a description the user passed directly.
func ResolveDescriptionFile(ios *iostreams.IOStreams, cmd *cobra.Command) error {
	if !cmd.Flags().Changed(descriptionFileFlag) {
		return nil
	}

	path, err := cmd.Flags().GetString(descriptionFileFlag)
	if err != nil {
		return err
	}

	content, err := readDescriptionFile(ios, path)
	if err != nil {
		return err
	}

	// --description uses "-" to open an editor, so a file holding exactly that
	// cannot be passed through without silently changing meaning.
	if content == "-" {
		return &FlagError{Err: fmt.Errorf("--%s content is only %q, which --%s uses to open an editor", descriptionFileFlag, "-", descriptionFlag)}
	}

	// Set marks --description as changed, so the callers' Changed("description")
	// checks treat a description read from a file the same as one passed inline.
	return cmd.Flags().Set(descriptionFlag, content)
}

func readDescriptionFile(ios *iostreams.IOStreams, path string) (_ string, err error) {
	if path == "-" {
		defer func() { err = errors.Join(err, ios.In.Close()) }()

		b, readErr := io.ReadAll(ios.In)
		if readErr != nil {
			return "", fmt.Errorf("failed to read description from standard input: %w", readErr)
		}
		return string(b), nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read description file: %w", err)
	}
	return string(b), nil
}
