//go:build !integration

package mrutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func Test_ParseLine(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantStart int
		wantEnd   int
		wantErr   string
	}{
		{name: "empty string", input: "", wantStart: 0, wantEnd: 0},
		{name: "single line", input: "42", wantStart: 42, wantEnd: 42},
		{name: "line range", input: "10:15", wantStart: 10, wantEnd: 15},
		{name: "same start and end", input: "5:5", wantStart: 5, wantEnd: 5},
		{name: "invalid number", input: "abc", wantErr: `invalid line number "abc"`},
		{name: "invalid range end", input: "10:abc", wantErr: `invalid line range "10:abc"`},
		{name: "reversed range", input: "15:10", wantErr: `invalid line range "15:10": end must be >= start`},
		{name: "zero line", input: "0", wantErr: "line number must be positive, got 0"},
		{name: "negative line", input: "-1", wantErr: "line number must be positive, got -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := ParseLine(tt.input)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantStart, start)
				assert.Equal(t, tt.wantEnd, end)
			}
		})
	}
}

func Test_FindFileDiff(t *testing.T) {
	version := &gitlab.MergeRequestDiffVersion{
		Diffs: []*gitlab.Diff{
			{NewPath: "src/main.go", OldPath: "src/main.go"},
			{NewPath: "src/new.go", OldPath: "src/old.go"},
		},
	}

	t.Run("match by NewPath", func(t *testing.T) {
		d, err := FindFileDiff(version, "src/main.go")
		require.NoError(t, err)
		assert.Equal(t, "src/main.go", d.NewPath)
	})

	t.Run("match by OldPath", func(t *testing.T) {
		d, err := FindFileDiff(version, "src/old.go")
		require.NoError(t, err)
		assert.Equal(t, "src/new.go", d.NewPath)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := FindFileDiff(version, "nonexistent.go")
		assert.EqualError(t, err, `file "nonexistent.go" not found in MR diff`)
	})
}

func Test_BuildDiffPosition(t *testing.T) {
	version := &gitlab.MergeRequestDiffVersion{
		BaseCommitSHA:  "base123",
		HeadCommitSHA:  "head456",
		StartCommitSHA: "start789",
	}

	// Simple diff: one removed, one added, one context line
	diffContent := `@@ -1,3 +1,3 @@
 unchanged line
-old line
+new line
 another unchanged
`

	fileDiff := &gitlab.Diff{
		NewPath: "file.go",
		OldPath: "file.go",
		Diff:    diffContent,
	}

	t.Run("new-side single line on added line", func(t *testing.T) {
		pos, err := BuildDiffPosition(version, fileDiff, 2, 2, 0)
		require.NoError(t, err)
		assert.Equal(t, "base123", *pos.BaseSHA)
		assert.Equal(t, "head456", *pos.HeadSHA)
		assert.Equal(t, "start789", *pos.StartSHA)
		assert.Equal(t, "file.go", *pos.NewPath)
		assert.Equal(t, int64(2), *pos.NewLine)
		// Added line has no OldLine
		assert.Nil(t, pos.OldLine)
		assert.Nil(t, pos.LineRange)
	})

	t.Run("new-side single line on unchanged line", func(t *testing.T) {
		pos, err := BuildDiffPosition(version, fileDiff, 1, 1, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), *pos.NewLine)
		assert.Equal(t, int64(1), *pos.OldLine) // Unchanged => both sides
		assert.Nil(t, pos.LineRange)
	})

	t.Run("new-side multiline range", func(t *testing.T) {
		pos, err := BuildDiffPosition(version, fileDiff, 1, 3, 0)
		require.NoError(t, err)
		// NewLine anchors on the end of the range (line 3)
		assert.Equal(t, int64(3), *pos.NewLine)
		// Line 3 is unchanged so OldLine is also set
		require.NotNil(t, pos.OldLine)
		assert.Equal(t, int64(3), *pos.OldLine)
		require.NotNil(t, pos.LineRange)
		assert.Equal(t, "new", *pos.LineRange.Start.Type)
		assert.Equal(t, int64(1), *pos.LineRange.Start.NewLine)
		// Start line 1 is unchanged, so OldLine is set
		require.NotNil(t, pos.LineRange.Start.OldLine)
		assert.Equal(t, int64(1), *pos.LineRange.Start.OldLine)
		assert.Equal(t, "new", *pos.LineRange.End.Type)
		assert.Equal(t, int64(3), *pos.LineRange.End.NewLine)
		// End line 3 is unchanged, so OldLine is set
		require.NotNil(t, pos.LineRange.End.OldLine)
		assert.Equal(t, int64(3), *pos.LineRange.End.OldLine)
	})

	t.Run("old-side line on removed line", func(t *testing.T) {
		pos, err := BuildDiffPosition(version, fileDiff, 0, 0, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), *pos.OldLine)
		assert.Nil(t, pos.NewLine) // Removed line => no new side
	})

	t.Run("old-side line on unchanged line", func(t *testing.T) {
		pos, err := BuildDiffPosition(version, fileDiff, 0, 0, 1)
		require.NoError(t, err)
		assert.Equal(t, int64(1), *pos.OldLine)
		assert.Equal(t, int64(1), *pos.NewLine) // Unchanged => both sides
	})

	t.Run("file-level comment", func(t *testing.T) {
		pos, err := BuildDiffPosition(version, fileDiff, 0, 0, 0)
		require.NoError(t, err)
		// Should target first line in diff (unchanged line 1)
		require.NotNil(t, pos.NewLine)
	})

	t.Run("line not in diff", func(t *testing.T) {
		_, err := BuildDiffPosition(version, fileDiff, 999, 999, 0)
		assert.ErrorContains(t, err, "line 999 not found in diff")
	})

	t.Run("old line not in diff", func(t *testing.T) {
		_, err := BuildDiffPosition(version, fileDiff, 0, 0, 999)
		assert.ErrorContains(t, err, "old line 999 not found in diff")
	})

	t.Run("multiline range end not in diff", func(t *testing.T) {
		_, err := BuildDiffPosition(version, fileDiff, 1, 999, 0)
		assert.ErrorContains(t, err, "line 999 not found in diff")
	})

	t.Run("file-level on delete-only diff", func(t *testing.T) {
		deleteDiff := &gitlab.Diff{
			NewPath: "deleted.go",
			OldPath: "deleted.go",
			Diff:    "@@ -1,2 +1 @@\n-removed1\n-removed2\n",
		}
		pos, err := BuildDiffPosition(version, deleteDiff, 0, 0, 0)
		require.NoError(t, err)
		require.NotNil(t, pos.OldLine)
		assert.Equal(t, int64(1), *pos.OldLine)
		assert.Nil(t, pos.NewLine)
	})

	t.Run("file-level on empty diff", func(t *testing.T) {
		emptyDiff := &gitlab.Diff{
			NewPath: "empty.go",
			OldPath: "empty.go",
			Diff:    "",
		}
		_, err := BuildDiffPosition(version, emptyDiff, 0, 0, 0)
		assert.ErrorContains(t, err, "diff for empty.go is empty")
	})
}

func Test_lineCode(t *testing.T) {
	assert.Equal(t, "a78c15ea253085032b9a8a057d8689b6fd7d0dfa_0_42", lineCode("file.go", 42))
}
