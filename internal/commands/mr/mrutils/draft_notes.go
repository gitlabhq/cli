package mrutils

import (
	"context"
	"errors"
	"fmt"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
)

// ListAllDraftNotes returns every pending review comment the current user has
// on the merge request.
func ListAllDraftNotes(ctx context.Context, client *gitlab.Client, projectID any, mrIID int64) ([]*gitlab.DraftNote, error) {
	opts := &gitlab.ListDraftNotesOptions{PerPage: api.MaxPerPage}
	drafts, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.DraftNote, *gitlab.Response, error) {
		return client.DraftNotes.ListDraftNotes(projectID, mrIID, opts, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pending review comments: %w", err)
	}
	return drafts, nil
}

// GetDraftNote returns one of the current user's pending review comments on
// the merge request.
func GetDraftNote(ctx context.Context, client *gitlab.Client, projectID any, mrIID, draftNoteID int64) (*gitlab.DraftNote, error) {
	draft, _, err := client.DraftNotes.GetDraftNote(projectID, mrIID, draftNoteID, gitlab.WithContext(ctx))
	if err != nil {
		if errors.Is(err, gitlab.ErrNotFound) {
			return nil, fmt.Errorf("pending review comment %d not found in merge request !%d", draftNoteID, mrIID)
		}
		return nil, fmt.Errorf("failed to get pending review comment %d: %w", draftNoteID, err)
	}
	return draft, nil
}

// HasFilePosition reports whether pos targets a file. The API returns general
// pending comments with an empty position object rather than none.
func HasFilePosition(pos *gitlab.NotePosition) bool {
	return pos != nil && (pos.NewPath != "" || pos.OldPath != "")
}
