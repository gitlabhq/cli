package infer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/git"
)

// parseCommitSelection removes comments and trims space for all non-comment lines
func parseCommitSelection(input string) ([]string, error) {
	result := []string{} // Initialize as empty slice, not nil

	for line := range strings.SplitSeq(input, "\n") {
		// Trim whitespace from the line first
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "#") {
			continue
		}

		if line == "" {
			continue
		}

		commitLine := strings.Fields(line)

		if (len(commitLine) == 1) || stackutils.HasComment(commitLine) {
			result = append(result, commitLine[0])
		} else {
			return []string{},
				fmt.Errorf("improperly formatted reorder file: unexpected content after commit hash on line %q", line)
		}
	}
	return result, nil
}

func promptForCommits(ctx context.Context, f cmdutils.Factory, gr git.GitRunner, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("no revision arguments provided")
	}

	// Join all args to pass to RevList, similar to how git rev-list works
	revArgs := args
	commitData, err := gr.Git(append([]string{"log", "--format=%s%x00%h%x00%an", "--reverse"}, revArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("could not get commit list: %w", err)
	}

	var buffer bytes.Buffer

	buffer.WriteString(heredoc.Doc(`
		# Choose which commits you'd like to include in your stack.
		# Lines starting with '#' will be ignored.
		# Each line should contain only the commit hash you want to include.
		# Remove or comment out commits you don't want in the stack.
		#
		# Commits are listed oldest to newest (stack order):
		#
	`))

	// Parse the commit data format: "subject\x00hash\x00author"
	for line := range strings.SplitSeq(strings.TrimSpace(commitData), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\x00")
		if len(parts) < 2 {
			continue
		}

		subject := parts[0]
		hash := parts[1]
		author := ""
		if len(parts) >= 3 {
			author = parts[2]
		}

		// Format: hash # subject (author)
		if author != "" {
			buffer.WriteString(fmt.Sprintf("%s # %s (%s)\n", hash, subject, author))
		} else {
			buffer.WriteString(fmt.Sprintf("%s # %s\n", hash, subject))
		}
	}

	editor, err := cmdutils.GetEditor(f.Config)
	if err != nil {
		return nil, err
	}

	if !f.IO().IsOutputTTY() {
		return nil, errors.New("no TTY available")
	}

	var promptResponse string
	err = f.IO().DirectEditor(ctx, &promptResponse, buffer.String(), editor)
	if err != nil {
		return nil, err
	}

	return parseCommitSelection(promptResponse)
}
