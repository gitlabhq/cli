package note

import (
	"fmt"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

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
