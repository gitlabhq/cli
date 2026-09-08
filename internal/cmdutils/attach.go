package cmdutils

import (
	"context"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/upload"
)

const attachFlag = "attach"

// AddAttachFlag registers --attach on cmd, bound to paths.
func AddAttachFlag(cmd *cobra.Command, paths *[]string, resource string) {
	cmd.Flags().StringArrayVar(paths, attachFlag, nil, fmt.Sprintf(
		"(EXPERIMENTAL) Upload a file and reference it at the end of the %s. Use \"-\" to read the file from standard input. Repeat the flag to attach multiple files.",
		resource,
	))
}

// checkAttachmentStdin rejects sharing stdin: reading the description closes
// the stream, leaving the attachment an exhausted reader.
func checkAttachmentStdin(cmd *cobra.Command, descriptionFile string) error {
	if descriptionFile != upload.StdinPath || cmd.Flags().Lookup(attachFlag) == nil {
		return nil
	}

	paths, err := cmd.Flags().GetStringArray(attachFlag)
	if err != nil {
		return err
	}
	if !slices.Contains(paths, upload.StdinPath) {
		return nil
	}

	return &FlagError{Err: fmt.Errorf("--%s and --%s cannot both read from %q", descriptionFileFlag, attachFlag, upload.StdinPath)}
}

// AppendAttachments uploads paths and appends a markdown reference to each.
//
// projectPath must be the project that renders the body, since that is the only
// one an upload reference resolves against. For a merge request from a fork
// that is the target project, not the head repo.
func AppendAttachments(ctx context.Context, ios *iostreams.IOStreams, client *gitlab.Client, projectPath, body string, paths []string) (string, error) {
	if len(paths) == 0 {
		return body, nil
	}

	attachments, err := upload.Attachments(ios.In, paths)
	if err != nil {
		return "", err
	}

	references, err := upload.Upload(ctx, ios, client, projectPath, attachments)
	if err != nil {
		return "", err
	}

	return upload.Append(body, references), nil
}

// AppendAttachmentsToUpdate appends to newBody, or to current when the user
// supplied no new body. current is called only when needed, so a caller that
// fetches the existing body spends no request when newBody made it redundant.
func AppendAttachmentsToUpdate(ctx context.Context, ios *iostreams.IOStreams, client *gitlab.Client, projectPath string, newBody *string, current func() (string, error), paths []string) (string, error) {
	if newBody != nil {
		return AppendAttachments(ctx, ios, client, projectPath, *newBody, paths)
	}

	body, err := current()
	if err != nil {
		return "", err
	}

	return AppendAttachments(ctx, ios, client, projectPath, body, paths)
}
