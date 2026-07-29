package mrutils

import (
	"fmt"
	"io"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type PrintDiscussionsOptions struct {
	ShowSystemLogs                 bool
	ShowSingleNoteDiscussionPrefix bool
}

// noteUsername returns the note author's username, falling back to "unknown"
// if the username is empty (e.g. redacted users or system-generated notes).
func noteUsername(n *gitlab.Note) string {
	if n.Author.Username != "" {
		return n.Author.Username
	}
	return "unknown"
}

// noteTimeAgo returns a human-readable timestamp for the note's creation time
// combining a relative "time ago" string with the absolute time, e.g.
// "1 day ago (2026-06-26 05:35:51)", or an empty string if CreatedAt is nil.
func noteTimeAgo(n *gitlab.Note) string {
	if n.CreatedAt == nil {
		return ""
	}
	return fmt.Sprintf("%s (%s)",
		utils.TimeToPrettyTimeAgo(*n.CreatedAt),
		n.CreatedAt.Format("2006-01-02 15:04:05"),
	)
}

// renderBody renders the body as markdown for a terminal, or returns it
// verbatim when piped so the raw output stays free of control characters.
func renderBody(ios *iostreams.IOStreams, body string) string {
	if !ios.IsOutputTTY() {
		return body
	}
	rendered, err := utils.RenderMarkdown(body, ios.BackgroundColor())
	if err != nil {
		return body
	}
	return rendered
}

// PrintDiscussions renders discussions to out.
func PrintDiscussions(out io.Writer, ios *iostreams.IOStreams, discussions []*gitlab.Discussion, opts PrintDiscussionsOptions) {
	c := ios.Color()

	for _, discussion := range discussions {
		if len(discussion.Notes) == 0 {
			continue
		}

		firstNote := discussion.Notes[0]

		// Skip system notes unless showSystemLogs is set
		if firstNote.System && !opts.ShowSystemLogs {
			continue
		}

		// Threaded discussions (not individual notes)
		if !discussion.IndividualNote && len(discussion.Notes) > 1 {
			fmt.Fprint(out, "Thread") //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			if discussion.ID != "" {
				fmt.Fprintf(out, " [discussion: %s]", TruncateDiscussionID(discussion.ID)) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			}

			// Show resolution status if resolvable
			if firstNote.Resolvable {
				if firstNote.Resolved {
					fmt.Fprint(out, c.Green(" ✓ resolved")) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				} else {
					fmt.Fprint(out, c.Yellow(" ⚠ unresolved")) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				}
			}
			fmt.Fprintln(out) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)

			// Print first note
			createdAt := noteTimeAgo(firstNote)
			fmt.Fprintf(out, "  @%s commented ", noteUsername(firstNote))                                   //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			fmt.Fprintf(out, "%s %s\n", c.Gray(createdAt), c.Gray(fmt.Sprintf("[note #%d]", firstNote.ID))) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)

			if firstNote.Position != nil {
				PrintCommentFileContext(out, c, firstNote.Position)
			}

			body := renderBody(ios, firstNote.Body)
			fmt.Fprintln(out, utils.Indent(body, "  ")) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			fmt.Fprintln(out)                           //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)

			// Print replies (indented)
			for i, note := range discussion.Notes[1:] {
				if note.System && !opts.ShowSystemLogs {
					continue
				}
				replyTime := noteTimeAgo(note)
				fmt.Fprintf(out, "    @%s replied ", noteUsername(note))                                   //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				fmt.Fprintf(out, "%s %s\n", c.Gray(replyTime), c.Gray(fmt.Sprintf("[note #%d]", note.ID))) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)

				replyBody := renderBody(ios, note.Body)
				fmt.Fprintln(out, utils.Indent(replyBody, "    ")) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				if i < len(discussion.Notes[1:])-1 {
					fmt.Fprintln(out) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				}
			}
			fmt.Fprintln(out) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
		} else {
			// Individual note (not a thread)
			note := firstNote
			createdAt := noteTimeAgo(note)
			fmt.Fprint(out, "@", noteUsername(note)) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			if note.System {
				fmt.Fprintf(out, " %s ", note.Body)  //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				fmt.Fprintln(out, c.Gray(createdAt)) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			} else {
				body := renderBody(ios, note.Body)
				noteLabel := fmt.Sprintf("[note #%d]", note.ID)
				if opts.ShowSingleNoteDiscussionPrefix && discussion.ID != "" {
					noteLabel += fmt.Sprintf(" [discussion: %s]", TruncateDiscussionID(discussion.ID))
				}
				fmt.Fprint(out, " commented ")                                    //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				fmt.Fprintf(out, "%s %s\n", c.Gray(createdAt), c.Gray(noteLabel)) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)

				if note.Position != nil {
					PrintCommentFileContext(out, c, note.Position)
				}

				fmt.Fprintln(out, utils.Indent(body, " ")) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
			}
			fmt.Fprintln(out) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
		}
	}
}

// PrintCommentFileContext prints file and line context for a note position.
func PrintCommentFileContext(out io.Writer, c *iostreams.ColorPalette, pos *gitlab.NotePosition) {
	// Check for multi-line comment first
	if pos.LineRange != nil && pos.LineRange.StartRange != nil && pos.LineRange.EndRange != nil {
		startLine := pos.LineRange.StartRange.NewLine
		endLine := pos.LineRange.EndRange.NewLine

		// Fall back to old line numbers if new ones aren't available
		if startLine == 0 {
			startLine = pos.LineRange.StartRange.OldLine
		}
		if endLine == 0 {
			endLine = pos.LineRange.EndRange.OldLine
		}

		// Display range if we have valid start and end lines
		if startLine > 0 && endLine > 0 {
			filePath := pos.NewPath
			if filePath == "" {
				filePath = pos.OldPath
			}
			if filePath != "" {
				if startLine != endLine {
					fmt.Fprintf(out, " on %s:%d-%d\n", c.Cyan(filePath), startLine, endLine) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				} else {
					fmt.Fprintf(out, " on %s:%d\n", c.Cyan(filePath), startLine) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
				}
				return
			}
		}
	}

	// Fall back to single-line comment
	if pos.NewPath != "" && pos.NewLine > 0 {
		fmt.Fprintf(out, " on %s:%d\n", c.Cyan(pos.NewPath), pos.NewLine) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
	} else if pos.OldPath != "" && pos.OldLine > 0 {
		fmt.Fprintf(out, " on %s:%d\n", c.Cyan(pos.OldPath), pos.OldLine) //nolint:forbidigo // out is a generic io.Writer also used with non-stdout writers (strings.Builder, bytes.Buffer)
	}
}
