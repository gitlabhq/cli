//go:build !integration

package mrutils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func newTimer() *time.Time {
	t, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	return &t
}

func Test_FilterDiscussions_ByState(t *testing.T) {
	t.Parallel()
	timer := newTimer()

	discussions := []*gitlab.Discussion{
		{
			ID: "disc1",
			Notes: []*gitlab.Note{
				{ID: 1, Resolvable: true, Resolved: true, CreatedAt: timer},
				{ID: 2, Resolvable: true, Resolved: true, CreatedAt: timer},
			},
		},
		{
			ID: "disc2",
			Notes: []*gitlab.Note{
				{ID: 3, Resolvable: true, Resolved: false, CreatedAt: timer},
			},
		},
		{
			ID: "disc3",
			Notes: []*gitlab.Note{
				{ID: 4, Resolvable: true, Resolved: true, CreatedAt: timer},
				{ID: 5, Resolvable: true, Resolved: false, CreatedAt: timer},
			},
		},
		{
			ID: "disc4",
			Notes: []*gitlab.Note{
				{ID: 6, Resolvable: false, CreatedAt: timer},
			},
		},
	}

	tests := []struct {
		name    string
		state   string
		wantIDs []string
	}{
		{"resolved only", "resolved", []string{"disc1"}},
		{"unresolved only", "unresolved", []string{"disc2", "disc3"}},
		{"resolvable (all with resolvable notes)", "resolvable", []string{"disc1", "disc2", "disc3"}},
		{"no filter", "", []string{"disc1", "disc2", "disc3", "disc4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FilterDiscussions(discussions, FilterOpts{State: tt.state})
			gotIDs := make([]string, len(got))
			for i, d := range got {
				gotIDs[i] = d.ID
			}
			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

func Test_FilterDiscussions_ByType(t *testing.T) {
	t.Parallel()
	timer := newTimer()

	discussions := []*gitlab.Discussion{
		{
			ID: "general",
			Notes: []*gitlab.Note{
				{ID: 1, System: false, Position: nil, CreatedAt: timer},
			},
		},
		{
			ID: "diff",
			Notes: []*gitlab.Note{
				{ID: 2, System: false, Position: &gitlab.NotePosition{NewPath: "file.go", NewLine: 10}, CreatedAt: timer},
			},
		},
		{
			ID: "system",
			Notes: []*gitlab.Note{
				{ID: 3, System: true, CreatedAt: timer},
			},
		},
	}

	tests := []struct {
		name    string
		typ     string
		wantIDs []string
	}{
		{"general", "general", []string{"general"}},
		{"diff", "diff", []string{"diff"}},
		{"system", "system", []string{"system"}},
		{"all", "", []string{"general", "diff", "system"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FilterDiscussions(discussions, FilterOpts{Type: tt.typ})
			gotIDs := make([]string, len(got))
			for i, d := range got {
				gotIDs[i] = d.ID
			}
			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

func Test_FilterDiscussions_ByFilePath(t *testing.T) {
	t.Parallel()
	timer := newTimer()

	discussions := []*gitlab.Discussion{
		{
			ID: "on-file-a",
			Notes: []*gitlab.Note{
				{ID: 1, Position: &gitlab.NotePosition{NewPath: "a.go", NewLine: 10}, CreatedAt: timer},
			},
		},
		{
			ID: "on-file-b",
			Notes: []*gitlab.Note{
				{ID: 2, Position: &gitlab.NotePosition{NewPath: "b.go", NewLine: 5}, CreatedAt: timer},
			},
		},
		{
			ID: "general",
			Notes: []*gitlab.Note{
				{ID: 3, Position: nil, CreatedAt: timer},
			},
		},
		{
			ID: "old-path-match",
			Notes: []*gitlab.Note{
				{ID: 4, Position: &gitlab.NotePosition{OldPath: "a.go", OldLine: 3}, CreatedAt: timer},
			},
		},
	}

	got := FilterDiscussions(discussions, FilterOpts{FilePath: "a.go"})
	require.Len(t, got, 2)
	assert.Equal(t, "on-file-a", got[0].ID)
	assert.Equal(t, "old-path-match", got[1].ID)
}

func Test_FilterDiscussions_Combined(t *testing.T) {
	t.Parallel()
	timer := newTimer()

	discussions := []*gitlab.Discussion{
		{
			ID: "resolved-diff-a",
			Notes: []*gitlab.Note{
				{ID: 1, Resolvable: true, Resolved: true, Position: &gitlab.NotePosition{NewPath: "a.go", NewLine: 10}, CreatedAt: timer},
			},
		},
		{
			ID: "unresolved-diff-a",
			Notes: []*gitlab.Note{
				{ID: 2, Resolvable: true, Resolved: false, Position: &gitlab.NotePosition{NewPath: "a.go", NewLine: 20}, CreatedAt: timer},
			},
		},
		{
			ID: "unresolved-diff-b",
			Notes: []*gitlab.Note{
				{ID: 3, Resolvable: true, Resolved: false, Position: &gitlab.NotePosition{NewPath: "b.go", NewLine: 5}, CreatedAt: timer},
			},
		},
	}

	got := FilterDiscussions(discussions, FilterOpts{
		State:    "unresolved",
		Type:     "diff",
		FilePath: "a.go",
	})
	require.Len(t, got, 1)
	assert.Equal(t, "unresolved-diff-a", got[0].ID)
}

func Test_FilterDiscussions_EmptyNotes(t *testing.T) {
	t.Parallel()
	discussions := []*gitlab.Discussion{
		{ID: "empty", Notes: []*gitlab.Note{}},
	}

	got := FilterDiscussions(discussions, FilterOpts{State: "resolved"})
	assert.Empty(t, got)
}
