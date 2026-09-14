package reorder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/git"
)

// parseReorderFile removes comments and trims space for all non-comment lines
func parseReorderFile(input string) ([]string, error) {
	branches := []string{}
	for line := range strings.SplitSeq(input, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}

		if line == "" {
			continue
		}

		branchLine := strings.Split(line, " ")

		if (len(branchLine) == 1) || stackutils.HasComment(branchLine) {
			branches = append(branches, strings.TrimSpace(branchLine[0]))
		} else {
			return []string{}, fmt.Errorf("improperly formatted reorder file: unexpected content after branch name on line %q", line)
		}

	}
	return branches, nil
}

func promptForOrder(ctx context.Context, f cmdutils.Factory, getText cmdutils.GetTextUsingEditor, stack git.Stack, branch string) ([]string, error) {
	var message string
	var buffer bytes.Buffer

	buffer.WriteString("# Arrange the stack references in the order you'd like. Lines starting\n# with '#' will be ignored. \n#\n")

	for ref := range stack.Iter() {
		currentBranch := " # "

		if branch == ref.Branch {
			currentBranch = " # <- Current branch, "
		}

		buffer.WriteString(ref.Branch + currentBranch + ref.Description + "\n")
	}

	message = buffer.String()

	editor, err := cmdutils.GetEditor(f.Config)
	if err != nil {
		return []string{}, err
	}

	var branches []string

	if !f.IO().IsOutputTTY() {
		return []string{}, errors.New("no TTY available")
	}

	promptResponse, err := getText(ctx, editor, "glab-stack-reorder*.gitrebase", message)
	if err != nil {
		return []string{}, err
	}

	branches, err = parseReorderFile(promptResponse)
	if err != nil {
		return []string{}, err
	}

	return branches, nil
}
