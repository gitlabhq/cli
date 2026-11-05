package help

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// Section is a titled block of command help that comes from an annotation.
type Section struct {
	Title string
	Body  string
}

var helpAnnotations = []struct{ key, title string }{
	{"help:arguments", "Arguments"},
	{"help:environment", "Environment variables"},
}

// AnnotationSections returns the help:arguments and help:environment sections
// of command, in display order. Annotations are not inherited, so a subcommand
// does not repeat its parent's sections.
func AnnotationSections(command *cobra.Command) []Section {
	var sections []Section
	for _, s := range helpAnnotations {
		if body := strings.Trim(command.Annotations[s.key], "\r\n"); body != "" {
			sections = append(sections, Section{Title: s.title, Body: body})
		}
	}
	return sections
}

// AppendAnnotationSections makes help for every command under root end with
// the command's annotation sections. fang.Execute replaces root's help func
// with fang's renderer, which ignores annotations, so the wrapper goes on
// root's direct children and every deeper command inherits it. Call it once
// the command tree is complete, before fang.Execute. A command that sets its
// own help func keeps the sections by calling its parent's help func.
//
// The wrapper replaces any help func already set on a direct child of root, so
// those children must not set one; TestTopLevelCommandsInheritRootHelpFunc in
// internal/commands enforces this. The sections are written to the command's
// OutOrStdout, where fang writes the help body, and streams only picks colors.
func AppendAnnotationSections(root *cobra.Command, streams *iostreams.IOStreams) {
	for _, child := range root.Commands() {
		child.SetHelpFunc(func(c *cobra.Command, args []string) {
			c.Root().HelpFunc()(c, args)
			writeAnnotationSections(c.OutOrStdout(), streams.Color(), c)
		})
	}
}

const (
	fangTitleIndent = "  "
	fangBodyIndent  = "    "
)

func writeAnnotationSections(w io.Writer, color *iostreams.ColorPalette, command *cobra.Command) {
	for _, s := range AnnotationSections(command) {
		fmt.Fprintln(w, fangTitleIndent+color.Bold(strings.ToUpper(s.Title))) //nolint:forbidigo // w is the command's own writer, where fang writes the help body
		fmt.Fprintln(w)                                                       //nolint:forbidigo // w is the command's own writer, where fang writes the help body
		fmt.Fprintln(w, utils.Indent(s.Body, fangBodyIndent))                 //nolint:forbidigo // w is the command's own writer, where fang writes the help body
		fmt.Fprintln(w)                                                       //nolint:forbidigo // w is the command's own writer, where fang writes the help body
	}
}
