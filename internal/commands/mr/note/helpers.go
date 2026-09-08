package note

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func getBodyFromStdinOrEditor(f cmdutils.Factory, cmd *cobra.Command) (string, error) {
	var body string

	if !f.IO().IsInTTY {
		data, err := io.ReadAll(f.IO().In)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		body = strings.TrimSpace(string(data))
	} else {
		editor, err := cmdutils.GetEditor(f.Config)
		if err != nil {
			return "", err
		}

		err = f.IO().Editor(cmd.Context(), &body, "Note message:", "Enter the note message for the merge request.", "", editor)
		if err != nil {
			return "", err
		}
	}

	return body, nil
}

// deduplicateNote checks whether a note with the same body already exists on the MR.
// If a duplicate is found, it prints the URL and returns (true, nil).
// If no duplicate is found, returns (false, nil).
func deduplicateNote(io *iostreams.IOStreams, client *gitlab.Client, repo string, mrIID int64, body, webURL string) (bool, error) {
	opts := &gitlab.ListMergeRequestNotesOptions{ListOptions: gitlab.ListOptions{PerPage: api.DefaultListLimit}}
	for {
		notes, resp, err := client.Notes.ListMergeRequestNotes(repo, mrIID, opts)
		if err != nil {
			return false, fmt.Errorf("failed to list merge request notes: %w", err)
		}
		for _, noteInfo := range notes {
			if strings.TrimSpace(noteInfo.Body) == strings.TrimSpace(body) {
				io.LogInfof("%s#note_%d\n", webURL, noteInfo.ID)
				return true, nil
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return false, nil
}
