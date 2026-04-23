// adapted from https://github.com/cli/cli/blob/trunk/pkg/cmd/pr/diff/diff.go
package diff

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	factory cmdutils.Factory
	io      *iostreams.IOStreams

	args     []string
	useColor string
	rawDiff  bool
}

func NewCmdDiff(f cmdutils.Factory, runF func(*options) error) *cobra.Command {
	opts := &options{
		factory: f,
		io:      f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "diff [<id> | <branch>]",
		Short: "View changes in a merge request.",
		Example: heredoc.Doc(`
			glab mr diff 123
			glab mr diff branch

			# Get merge request from current branch
			glab mr diff

			glab mr diff 123 --color=never`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(cmd); err != nil {
				return err
			}

			if runF != nil {
				return runF(opts)
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&opts.useColor, "color", "auto", "Use color in diff output: always, never, auto.")
	cmd.Flags().BoolVar(&opts.rawDiff, "raw", false, "Use raw diff format that can be piped to commands.")

	return cmd
}

func (o *options) complete(args []string) {
	if len(args) > 0 {
		o.args = args
	}

	if o.useColor == "auto" && !o.io.IsaTTY {
		o.useColor = "never"
	}
}

func (o *options) validate(cmd *cobra.Command) error {
	if repoOverride, _ := cmd.Flags().GetString("repo"); repoOverride != "" && len(o.args) == 0 {
		return &cmdutils.FlagError{Err: errors.New("argument required when using the --repo flag.")}
	}

	if !validColorFlag(o.useColor) {
		return &cmdutils.FlagError{Err: fmt.Errorf("did not understand color: %q. Expected one of 'always', 'never', or 'auto'.", o.useColor)}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.factory.GitLabClient()
	if err != nil {
		return err
	}
	mr, baseRepo, err := mrutils.MRFromArgs(ctx, o.factory, o.args, "any")
	if err != nil {
		return err
	}

	diffOut := &bytes.Buffer{}

	if o.rawDiff {
		rawDiff, _, err := client.MergeRequests.ShowMergeRequestRawDiffs(baseRepo.FullName(), mr.IID, nil)
		if err != nil {
			return fmt.Errorf("could not obtain raw diff: %w", err)
		}

		diffOut.Write(rawDiff)
	} else {
		diffs, _, err := client.MergeRequests.GetMergeRequestDiffVersions(baseRepo.FullName(), mr.IID, &gitlab.GetMergeRequestDiffVersionsOptions{})
		if err != nil {
			return fmt.Errorf("could not find merge request diffs: %w", err)
		}
		if len(diffs) == 0 {
			return fmt.Errorf("no merge request diffs found")
		}

		// diff versions are returned by the API in order of most recent first
		diff := diffs[0]

		// the diffs are not included in the GetMergeRequestDiffVersions so we query for the diff version
		diffVersion, _, err := client.MergeRequests.GetSingleMergeRequestDiffVersion(baseRepo.FullName(), mr.IID, diff.ID, &gitlab.GetSingleMergeRequestDiffVersionOptions{})
		if err != nil {
			return fmt.Errorf("could not find merge request diff: %w", err)
		}
		for _, diffLine := range diffVersion.Diffs {
			// output the unified diff header
			diffOut.WriteString("--- " + diffLine.OldPath + "\n")
			diffOut.WriteString("+++ " + diffLine.NewPath + "\n")

			diffOut.WriteString(diffLine.Diff)
		}

		defer diffOut.Reset()
	}

	err = o.io.StartPager()
	if err != nil {
		return err
	}
	defer o.io.StopPager()

	if o.useColor == "never" {
		_, err = io.Copy(o.io.StdOut, diffOut)
		if errors.Is(err, syscall.EPIPE) {
			return nil
		}
		return err
	}

	diffLines := bufio.NewScanner(diffOut)
	for diffLines.Scan() {
		diffLine := diffLines.Text()
		switch {
		case isHeaderLine(diffLine):
			fmt.Fprintf(o.io.StdOut, "\x1b[1;38m%s\x1b[m\n", diffLine)
		case isAdditionLine(diffLine):
			fmt.Fprintf(o.io.StdOut, "\x1b[32m%s\x1b[m\n", diffLine)
		case isRemovalLine(diffLine):
			fmt.Fprintf(o.io.StdOut, "\x1b[31m%s\x1b[m\n", diffLine)
		default:
			fmt.Fprintln(o.io.StdOut, diffLine)
		}
	}

	if err := diffLines.Err(); err != nil {
		return fmt.Errorf("error reading merge request diff: %w", err)
	}

	return nil
}

var diffHeaderPrefixes = []string{"+++", "---", "diff", "index"}

func isHeaderLine(dl string) bool {
	for _, p := range diffHeaderPrefixes {
		if strings.HasPrefix(dl, p) {
			return true
		}
	}
	return false
}

func isAdditionLine(dl string) bool {
	return strings.HasPrefix(dl, "+")
}

func isRemovalLine(dl string) bool {
	return strings.HasPrefix(dl, "-")
}

func validColorFlag(c string) bool {
	return c == "auto" || c == "always" || c == "never"
}
