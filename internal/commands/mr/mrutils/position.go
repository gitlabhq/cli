package mrutils

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/diff"
)

// LineRange is the value of a --line flag: a single new-side line like "42"
// or a range like "10:15". It parses during flag parsing, so a malformed value
// fails before a command prompts for a comment body. The zero value means no
// line.
type LineRange struct {
	Start, End int
}

func (r *LineRange) String() string {
	switch {
	case r.Start == 0:
		return ""
	case r.Start == r.End:
		return strconv.Itoa(r.Start)
	default:
		return fmt.Sprintf("%d:%d", r.Start, r.End)
	}
}

func (r *LineRange) Set(s string) error {
	start, end, err := parseLine(s)
	if err != nil {
		return err
	}
	r.Start, r.End = start, end
	return nil
}

func (r *LineRange) Type() string {
	return "string"
}

// DiffPosition resolves a file path and a --line or --old-line value into a
// diff position on the latest merge request diff version. Leave lines and
// oldLine zero for a file-level comment.
func DiffPosition(ctx context.Context, client *gitlab.Client, project string, mrIID int64, filePath string, lines LineRange, oldLine int) (*gitlab.PositionOptions, error) {
	version, err := latestDiffVersion(ctx, client, project, mrIID)
	if err != nil {
		return nil, err
	}

	fileDiff, err := findFileDiff(version, filePath)
	if err != nil {
		return nil, err
	}

	return buildDiffPosition(version, fileDiff, lines.Start, lines.End, oldLine)
}

// latestDiffVersion returns the latest MR diff version with its diffs included.
func latestDiffVersion(ctx context.Context, client *gitlab.Client, project string, mrIID int64) (*gitlab.MergeRequestDiffVersion, error) {
	versions, _, err := client.MergeRequests.GetMergeRequestDiffVersions(project, mrIID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list MR diff versions: %w", err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no diff versions found for MR !%d", mrIID)
	}
	// The API lists versions newest first.
	latest := versions[0]
	full, _, err := client.MergeRequests.GetSingleMergeRequestDiffVersion(project, mrIID, latest.ID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch diff version %d: %w", latest.ID, err)
	}
	return full, nil
}

// findFileDiff finds a file's diff in the given version by matching NewPath or OldPath.
func findFileDiff(version *gitlab.MergeRequestDiffVersion, filePath string) (*gitlab.Diff, error) {
	for _, d := range version.Diffs {
		if d.NewPath == filePath || d.OldPath == filePath {
			return d, nil
		}
	}
	return nil, fmt.Errorf("file %q not found in MR diff", filePath)
}

// buildDiffPosition builds a PositionOptions for a diff comment.
// lineStart/lineEnd refer to new-side lines (lineEnd > lineStart for multiline).
// oldLine refers to an old-side (removed) line.
// For file-level comments, pass lineStart=0 and oldLine=0.
//
// When targeting an "unchanged" (context) line, both OldLine and NewLine must be
// set in the position — GitLab's API requires both sides for context lines.
// For added lines only NewLine is set; for removed lines only OldLine.
// Reference implementation: GitLab VS Code Extension, see
// https://gitlab.com/gitlab-org/gitlab-vscode-extension/-/blob/main/src/common/services/mr/create_comment.ts
func buildDiffPosition(version *gitlab.MergeRequestDiffVersion, fileDiff *gitlab.Diff, lineStart, lineEnd, oldLine int) (*gitlab.PositionOptions, error) {
	pos := &gitlab.PositionOptions{
		BaseSHA:      new(version.BaseCommitSHA),
		HeadSHA:      new(version.HeadCommitSHA),
		StartSHA:     new(version.StartCommitSHA),
		NewPath:      new(fileDiff.NewPath),
		OldPath:      new(fileDiff.OldPath),
		PositionType: new("text"),
	}

	lines := diff.Parse(fileDiff.Diff)

	switch {
	case lineStart == 0 && oldLine == 0:
		// File-level comment: target first available line
		if len(lines) == 0 {
			return nil, fmt.Errorf("diff for %s is empty, cannot place a comment", fileDiff.NewPath)
		}
		for _, l := range lines {
			switch l.Type {
			case diff.Added:
				pos.NewLine = new(int64(l.NewLine))
				return pos, nil
			case diff.Removed:
				pos.OldLine = new(int64(l.OldLine))
				return pos, nil
			case diff.Unchanged:
				if l.NewLine > 0 {
					pos.NewLine = new(int64(l.NewLine))
					pos.OldLine = new(int64(l.OldLine))
					return pos, nil
				}
			}
		}
		return nil, fmt.Errorf("no targetable line found in diff for %s", fileDiff.NewPath)

	case oldLine > 0:
		// Targeting an old-side (removed) line.
		correspondingNewLine, lt, err := diff.FindOldLine(lines, oldLine)
		if err != nil {
			return nil, fmt.Errorf("old line %d not found in diff for %s", oldLine, fileDiff.OldPath)
		}
		pos.OldLine = new(int64(oldLine))
		if lt == diff.Unchanged {
			pos.NewLine = new(int64(correspondingNewLine))
		}

	case lineEnd > lineStart:
		// Multiline range on the new side.
		// GitLab attaches the note at new_line/old_line, so those must
		// reference the *end* of the range; line_range defines the
		// highlighted span.
		startOld, startLT, err := diff.FindNewLine(lines, lineStart)
		if err != nil {
			return nil, fmt.Errorf("line %d not found in diff for %s", lineStart, fileDiff.NewPath)
		}
		endOld, endLT, err := diff.FindNewLine(lines, lineEnd)
		if err != nil {
			return nil, fmt.Errorf("line %d not found in diff for %s", lineEnd, fileDiff.NewPath)
		}
		pos.NewLine = new(int64(lineEnd))
		if endLT == diff.Unchanged {
			pos.OldLine = new(int64(endOld))
		}

		startPos := &gitlab.LinePositionOptions{
			LineCode: new(lineCode(fileDiff.NewPath, lineStart)),
			Type:     new("new"),
			NewLine:  new(int64(lineStart)),
		}
		if startLT == diff.Unchanged {
			startPos.OldLine = new(int64(startOld))
		}

		endPos := &gitlab.LinePositionOptions{
			LineCode: new(lineCode(fileDiff.NewPath, lineEnd)),
			Type:     new("new"),
			NewLine:  new(int64(lineEnd)),
		}
		if endLT == diff.Unchanged {
			endPos.OldLine = new(int64(endOld))
		}

		pos.LineRange = &gitlab.LineRangeOptions{
			Start: startPos,
			End:   endPos,
		}

	default:
		// Single new-side line.
		oldLineNum, lt, err := diff.FindNewLine(lines, lineStart)
		if err != nil {
			return nil, fmt.Errorf("line %d not found in diff for %s", lineStart, fileDiff.NewPath)
		}
		pos.NewLine = new(int64(lineStart))
		if lt == diff.Unchanged {
			pos.OldLine = new(int64(oldLineNum))
		}
	}

	return pos, nil
}

// parseLine parses a line flag value like "42" or "10:15" into start and end line numbers.
// For a single line, start == end.
func parseLine(s string) (int, int, error) {
	if s == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(s, ":", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid line number %q", s)
	}
	if start <= 0 {
		return 0, 0, fmt.Errorf("line number must be positive, got %d", start)
	}
	if len(parts) == 2 {
		end, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid line range %q", s)
		}
		if end < start {
			return 0, 0, fmt.Errorf("invalid line range %q: end must be >= start", s)
		}
		return start, end, nil
	}
	return start, start, nil
}

// lineCode generates a GitLab line_code for multiline ranges.
// Format: sha1(file_path)_oldline_newline
func lineCode(path string, line int) string {
	h := sha1.Sum([]byte(path))
	return fmt.Sprintf("%x_%d_%d", h, 0, line)
}
