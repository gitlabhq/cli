package help

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// getGlamourStyle returns the glamour style to use, replicating glamour v1's
// auto-detection logic that was removed in v2. It checks the GLAMOUR_STYLE
// environment variable first, then falls back to notty for non-TTY output
// or dark/light based on terminal background color.
func getGlamourStyle() string {
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" {
		return s
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return styles.NoTTYStyle
	}
	if termenv.HasDarkBackground() {
		return styles.DarkStyle
	}
	return styles.LightStyle
}

// renderWithGlamour renders markdown text using Glamour
func renderWithGlamour(text string) string {
	if text == "" {
		return ""
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(getGlamourStyle()),
		glamour.WithWordWrap(120),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return text
	}

	renderedText, err := renderer.Render(text)
	if err != nil {
		return text
	}

	return strings.TrimSpace(renderedText)
}

// This code forked from the GitHub CLI at http://github.com/cli - cli/internal/text/indent.go
// https://github.com/cli/cli/blob/929e082c13909044e2585af292ae952c9ca6f25c/pkg/text/indent.go

// MIT License
//
// Copyright (c) 2019 GitHub Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// RootUsageFunc renders a command's usage to out. Callers route diagnostic
// usage messages to stderr; see NewCmdRoot for the wiring and
// gitlab-org/cli#8371 for why we no longer rely on cobra's c.Print/SetOut.
func RootUsageFunc(out io.Writer, command *cobra.Command) error {
	fmt.Fprintf(out, "Usage:  %s", command.UseLine()) //nolint:forbidigo // out is a generic io.Writer shared with test buffers

	subcommands := command.Commands()
	if len(subcommands) > 0 {
		fmt.Fprint(out, "\n\nAvailable commands:\n") //nolint:forbidigo // out is a generic io.Writer shared with test buffers
		for _, c := range subcommands {
			if !c.IsAvailableCommand() {
				continue
			}
			fmt.Fprintf(out, "  %s\n", c.Name()) //nolint:forbidigo // out is a generic io.Writer shared with test buffers
		}
		return nil
	}

	flagUsages := command.LocalFlags().FlagUsages()
	if flagUsages != "" {
		fmt.Fprintln(out, "\n\nFlags:")                         //nolint:forbidigo // out is a generic io.Writer shared with test buffers
		fmt.Fprint(out, utils.Indent(dedent(flagUsages), "  ")) //nolint:forbidigo // out is a generic io.Writer shared with test buffers
	}
	return nil
}

var hasFailed bool

// HasFailed signals that the main process should exit with non-zero status
func HasFailed() bool {
	return hasFailed
}

// Display helpful error message in case subcommand name was mistyped.
// This matches Cobra's behavior for root command, which Cobra
// confusingly doesn't apply to nested commands.
func nestedSuggestFunc(out io.Writer, command *cobra.Command, arg string) {
	fmt.Fprintf(out, "unknown command %q for %q\n", arg, command.CommandPath()) //nolint:forbidigo // out is a generic io.Writer shared with test buffers

	var candidates []string
	if arg == "help" {
		candidates = []string{"--help"}
	} else {
		if command.SuggestionsMinimumDistance <= 0 {
			command.SuggestionsMinimumDistance = 2
		}
		candidates = command.SuggestionsFor(arg)
	}

	if len(candidates) > 0 {
		fmt.Fprint(out, "\nDid you mean this?\n") //nolint:forbidigo // out is a generic io.Writer shared with test buffers
		for _, c := range candidates {
			fmt.Fprintf(out, "\t%s\n", c) //nolint:forbidigo // out is a generic io.Writer shared with test buffers
		}
	}

	fmt.Fprint(out, "\n") //nolint:forbidigo // out is a generic io.Writer shared with test buffers
	_ = RootUsageFunc(out, command)
}

func isRootCmd(command *cobra.Command) bool {
	return command != nil && !command.HasParent()
}

// RootHelpFunc renders help text for the given command. Help is a
// successful-output channel, so it is written to streams.StdOut. The "unknown
// command" suggestion path is diagnostic and is routed to streams.StdErr. See
// NewCmdRoot for the wiring and gitlab-org/cli#8371 for the motivation.
func RootHelpFunc(streams *iostreams.IOStreams, command *cobra.Command, args []string) {
	c := streams.Color()
	originalLong := command.Long
	originalExample := command.Example

	if command.Long != "" {
		command.Long = renderWithGlamour(command.Long)
	}
	if command.Example != "" {
		command.Example = renderWithGlamour(command.Example)
	}

	if isRootCmd(command.Parent()) && len(args) >= 2 && args[1] != "--help" && args[1] != "-h" {
		nestedSuggestFunc(streams.StdErr, command, args[1])
		hasFailed = true

		command.Long = originalLong
		command.Example = originalExample
		return
	}

	var coreCommands []string
	var additionalCommands []string
	for _, c := range command.Commands() {
		if c.Short == "" {
			continue
		}
		if !c.IsAvailableCommand() {
			continue
		}

		s := rpad(c.Name()+":", c.NamePadding()) + c.Short
		if _, ok := c.Annotations["IsCore"]; ok {
			coreCommands = append(coreCommands, s)
		} else {
			additionalCommands = append(additionalCommands, s)
		}
	}

	// If there are no core commands, assume everything is a core command
	if len(coreCommands) == 0 {
		coreCommands = additionalCommands
		additionalCommands = []string{}
	}

	type helpEntry struct {
		Title string
		Body  string
	}

	var helpEntries []helpEntry
	if command.Long != "" {
		helpEntries = append(helpEntries, helpEntry{"", command.Long})
	} else if command.Short != "" {
		helpEntries = append(helpEntries, helpEntry{"", command.Short})
	}
	helpEntries = append(helpEntries, helpEntry{"USAGE", command.UseLine()})
	if len(command.Aliases) > 0 {
		helpEntries = append(helpEntries, helpEntry{"ALIASES", dedent(strings.Join(command.Aliases, ", "))})
	}
	if len(coreCommands) > 0 {
		helpEntries = append(helpEntries, helpEntry{"CORE COMMANDS", strings.Join(coreCommands, "\n")})
	}
	if len(additionalCommands) > 0 {
		helpEntries = append(helpEntries, helpEntry{"ADDITIONAL COMMANDS", strings.Join(additionalCommands, "\n")})
	}

	flagUsages := command.LocalFlags().FlagUsages()
	if flagUsages != "" {
		helpEntries = append(helpEntries, helpEntry{"FLAGS", dedent(flagUsages)})
	}
	inheritedFlagUsages := command.InheritedFlags().FlagUsages()
	if inheritedFlagUsages != "" {
		helpEntries = append(helpEntries, helpEntry{"INHERITED FLAGS", dedent(inheritedFlagUsages)})
	}
	if command.Example != "" {
		helpEntries = append(helpEntries, helpEntry{"EXAMPLES", command.Example})
	}
	for _, s := range AnnotationSections(command) {
		helpEntries = append(helpEntries, helpEntry{strings.ToUpper(s.Title), s.Body})
	}
	helpEntries = append(helpEntries, helpEntry{"LEARN MORE", `
Use 'glab <command> <subcommand> --help' for more information about a command.`})
	if _, ok := command.Annotations["help:feedback"]; ok {
		helpEntries = append(helpEntries, helpEntry{"FEEDBACK", command.Annotations["help:feedback"]})
	}

	for _, e := range helpEntries {
		if e.Title != "" {
			// If there is a title, add indentation to each line in the body
			streams.LogInfo(c.Bold(e.Title))
			streams.LogInfo(utils.Indent(strings.Trim(e.Body, "\r\n"), "  "))
		} else {
			// If there is no title print the body as is
			streams.LogInfo(e.Body)
		}
		streams.LogInfo()
	}

	command.Long = originalLong
	command.Example = originalExample
}

// rpad adds padding to the right of a string.
func rpad(s string, padding int) string {
	template := fmt.Sprintf("%%-%ds ", padding)
	return fmt.Sprintf(template, s)
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	minIndent := -1

	for _, l := range lines {
		if l == "" {
			continue
		}

		indent := len(l) - len(strings.TrimLeft(l, " "))
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent <= 0 {
		return s
	}

	var buf bytes.Buffer
	for _, l := range lines {
		fmt.Fprintln(&buf, strings.TrimPrefix(l, strings.Repeat(" ", minIndent))) //nolint:forbidigo // writing to a bytes.Buffer, not stdout/stderr
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
