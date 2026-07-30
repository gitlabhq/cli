package iostreams

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/briandowns/spinner"
	"github.com/google/shlex"
	"github.com/muesli/termenv"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/theme"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// ErrUserCancelled is returned when the user cancels a prompt (e.g., by pressing Ctrl+C)
var ErrUserCancelled = errors.New("user cancelled")

type IOStreams struct {
	In     io.ReadCloser
	StdOut io.Writer
	StdErr io.Writer

	IsaTTY         bool // stdout is a tty
	IsErrTTY       bool // stderr is a tty
	IsInTTY        bool // stdin is a tty
	promptDisabled bool // disable prompting for input

	is256ColorEnabled bool

	pagerCommand string
	pagerProcess *os.Process
	systemStdOut io.Writer

	spinner *spinner.Spinner

	backgroundColor string

	displayHyperlinks string

	isColorEnabled bool

	programOptions []tea.ProgramOption

	// JQ is the filter applied to JSON output by PrintJSON. It is always
	// non-nil so callers can pass &s.JQ to cobra's Flags().Var without nil
	// checks; IsActive reports whether a --jq expression has been supplied.
	JQ *JQFilter

	// outputFormat is recorded by cmdutils.EnableJSONOutput during flag
	// parsing, so that code holding no *cobra.Command can still honour the
	// requested format. One command runs per process.
	outputFormat string
}

var controlCharRegEx = regexp.MustCompile(`(\x1b\[)((?:(\d*)(;*))*)([A-Z,a-l,n-z])`)

// IOStreamsOption represents a function that configures io streams
type IOStreamsOption func(*IOStreams)

func WithStdin(stdin io.ReadCloser, isTTY bool) IOStreamsOption {
	return func(i *IOStreams) {
		if stdin != nil {
			i.In = stdin
		}
		i.IsInTTY = isTTY
	}
}

func WithStdout(stdout io.Writer, isTTY bool) IOStreamsOption {
	return func(i *IOStreams) {
		if stdout != nil {
			i.StdOut = stdout
			i.systemStdOut = stdout // TODO: is this really correct?!
		}
		i.IsaTTY = isTTY
	}
}

func WithStderr(stderr io.Writer, isTTY bool) IOStreamsOption {
	return func(i *IOStreams) {
		if stderr != nil {
			i.StdErr = stderr
		}
		i.IsErrTTY = isTTY
	}
}

func WithProgramOptions(opts ...tea.ProgramOption) IOStreamsOption {
	return func(i *IOStreams) {
		i.programOptions = append(i.programOptions, opts...)
	}
}

func WithPagerCommand(command string) IOStreamsOption {
	return func(i *IOStreams) {
		i.pagerCommand = command
	}
}

func WithDisplayHyperLinks(displayHyperlinks string) IOStreamsOption {
	return func(i *IOStreams) {
		if displayHyperlinks != "" {
			i.displayHyperlinks = displayHyperlinks
		}
	}
}

func New(options ...IOStreamsOption) *IOStreams {
	iostreams := &IOStreams{
		// static configuration that we don't need to change in tests.
		is256ColorEnabled: is256ColorSupported(),
		displayHyperlinks: "auto",
		JQ:                &JQFilter{},
	}

	// Apply options
	for _, option := range options {
		option(iostreams)
	}

	// configure static fields that rely on option functions
	iostreams.isColorEnabled = detectIsColorEnabled() && iostreams.IsaTTY && iostreams.IsErrTTY

	return iostreams
}

func stripControlCharacters(input string) string {
	return controlCharRegEx.ReplaceAllString(input, "^[[$2$5")
}

func writePagerOutput(dst io.Writer, src io.Reader) error {
	reader := bufio.NewReader(src)

	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if _, err := fmt.Fprintln(dst, stripControlCharacters(line)); err != nil {
				return err
			}
		}

		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (s *IOStreams) PromptEnabled() bool {
	if s.promptDisabled {
		return false
	}
	return s.IsOutputTTY()
}

func (s *IOStreams) ColorEnabled() bool {
	return s.isColorEnabled
}

func (s *IOStreams) Is256ColorSupported() bool {
	return s.is256ColorEnabled
}

func (s *IOStreams) SetPrompt(promptDisabled string) {
	switch promptDisabled {
	case "true", "1":
		s.promptDisabled = true
	case "false", "0":
		s.promptDisabled = false
	}
}

// SetOutputFormat records the output format the running command was invoked
// with, as parsed from its --output/-F flag.
func (s *IOStreams) SetOutputFormat(format string) {
	s.outputFormat = format
}

// IsJSONOutput reports whether the running command was asked to emit JSON.
func (s *IOStreams) IsJSONOutput() bool {
	return s.outputFormat == "json"
}

func (s *IOStreams) SetPager(cmd string) {
	s.pagerCommand = cmd
}

func (s *IOStreams) StartPager() error {
	if strings.TrimSpace(s.pagerCommand) == "" || s.pagerCommand == "cat" || !s.IsaTTY {
		return nil
	}

	pagerArgs, err := shlex.Split(s.pagerCommand)
	if err != nil {
		return err
	}

	pagerEnv := os.Environ()
	for i, v := range slices.Backward(pagerEnv) {
		if strings.HasPrefix(v, "PAGER=") {
			pagerEnv = append(pagerEnv[0:i], pagerEnv[i+1:]...)
		}
	}

	pagerEnv = append(pagerEnv, "LESSSECURE=1")

	// Only use `-r` (all raw control chars) when hyperlinks are forced on;
	// otherwise `-R` (color escapes only) avoids letting unexpected sequences
	// through to less.
	if s.displayHyperlinks == "always" {
		pagerEnv = append(pagerEnv, "LESS=FrX")
	} else if _, ok := os.LookupEnv("LESS"); !ok {
		pagerEnv = append(pagerEnv, "LESS=FRX")
	}
	if _, ok := os.LookupEnv("LV"); !ok {
		pagerEnv = append(pagerEnv, "LV=-c")
	}

	pagerCmd := exec.Command(pagerArgs[0], pagerArgs[1:]...)
	pagerCmd.Env = pagerEnv
	pagerCmd.Stdout = s.StdOut
	pagerCmd.Stderr = s.StdErr
	pagedOut, err := pagerCmd.StdinPipe()
	if err != nil {
		return err
	}

	pipeReader, pipeWriter := io.Pipe()
	s.StdOut = pipeWriter

	// TODO: Unfortunately, trying to add an error channel introduces a wait that locks up the code.
	// We should eventually add some error reporting for the go function

	go func() {
		defer pagedOut.Close()
		if err := writePagerOutput(pagedOut, pipeReader); err != nil {
			dbg.Debugf("pager output error: %v", err)
		}
	}()

	err = pagerCmd.Start()
	if err != nil {
		return err
	}
	s.pagerProcess = pagerCmd.Process

	go func() {
		_, _ = s.pagerProcess.Wait()
		_ = pipeWriter.Close()
	}()

	return nil
}

func (s *IOStreams) StopPager() {
	if s.pagerProcess == nil {
		return
	}

	if closer, ok := s.StdOut.(io.WriteCloser); ok {
		_ = closer.Close()
	}
	_, _ = s.pagerProcess.Wait()
	s.StdOut = s.systemStdOut
	s.pagerProcess = nil
}

func (s *IOStreams) StartSpinner(format string, a ...any) {
	if s.IsOutputTTY() {
		s.spinner = spinner.New(spinner.CharSets[9], 100*time.Millisecond, spinner.WithWriter(s.StdErr))
		if format != "" {
			s.spinner.Suffix = fmt.Sprintf(" "+format, a...)
		}
		s.spinner.Start()
	}
}

func (s *IOStreams) StopSpinner(format string, a ...any) {
	if s.spinner != nil {
		s.spinner.Suffix = ""
		s.spinner.FinalMSG = fmt.Sprintf(format, a...)
		s.spinner.Stop()
		s.spinner = nil
	}
}

func (s *IOStreams) TerminalWidth() int {
	return TerminalWidth(s.StdOut)
}

// IsOutputTTY returns true if both stdout and stderr are TTYs.
// Use this for output formatting decisions (tables vs plain text).
// For interactive prompts, use PromptEnabled() or IsInteractive() instead.
func (s *IOStreams) IsOutputTTY() bool {
	return s.IsErrTTY && s.IsaTTY
}

// IsInteractive returns true if the environment supports interactive prompts that require user input.
// This checks if stdin is a TTY and prompts are enabled (stdout/stderr are TTYs and prompts are not
// disabled with GLAB_NO_PROMPT or the no_prompt config).
// Use this before displaying interactive forms/menus that need to read user input (e.g., huh forms).
func (s *IOStreams) IsInteractive() bool {
	return s.IsInTTY && s.PromptEnabled()
}

func (s *IOStreams) ResolveBackgroundColor(style string) string {
	styleEnvVar := os.Getenv("GLAB_GLAMOUR_STYLE")
	deprecatedStyleEnvVar := os.Getenv("GLAMOUR_STYLE")

	if styleEnvVar != "" && style != "auto" {
		s.backgroundColor = styleEnvVar
		return styleEnvVar
	}

	if deprecatedStyleEnvVar != "" && style != "auto" {
		utils.PrintDeprecationWarning("GLAMOUR_STYLE")
		s.backgroundColor = deprecatedStyleEnvVar
		return deprecatedStyleEnvVar
	}

	// if we aren't using env vars we use the value from the config
	if styleEnvVar == "" && deprecatedStyleEnvVar == "" {
		s.backgroundColor = style
		return style
	}

	if (!s.ColorEnabled()) || (s.pagerProcess != nil) {
		s.backgroundColor = "none"
		return "none"
	}

	if termenv.HasDarkBackground() {
		s.backgroundColor = "dark"
		return "dark"
	}

	s.backgroundColor = "light"
	return "light"
}

func (s *IOStreams) BackgroundColor() string {
	if s.backgroundColor == "" {
		return "none"
	}
	return s.backgroundColor
}

// DisplayHyperlinks returns the current hyperlink display mode.
// One of "always", "auto", or "never".
func (s *IOStreams) DisplayHyperlinks() string {
	return s.displayHyperlinks
}

func (s *IOStreams) SetDisplayHyperlinks(displayHyperlinks string) {
	s.displayHyperlinks = displayHyperlinks
}

func (s *IOStreams) shouldDisplayHyperlinks() bool {
	switch s.displayHyperlinks {
	case "always":
		return true
	case "auto":
		return s.IsaTTY && s.pagerProcess == nil
	default:
		return false
	}
}

func (s *IOStreams) Hyperlink(displayText, targetURL string) string {
	if !s.shouldDisplayHyperlinks() {
		return displayText
	}

	openSequence := fmt.Sprintf("\x1b]8;;%s\x1b\\", targetURL)
	closeSequence := "\x1b]8;;\x1b\\"

	return openSequence + displayText + closeSequence
}

func (s *IOStreams) Confirm(ctx context.Context, result *bool, title string) error {
	return s.Run(ctx,
		huh.NewConfirm().
			Title(title).
			Affirmative("Yes!").
			Negative("No.").
			Value(result))
}

func (s *IOStreams) Input(ctx context.Context, result *string, title, placeholder string, validator func(string) error) error {
	input := huh.NewInput().
		Title(title).
		Value(result)

	if placeholder != "" {
		input = input.Placeholder(placeholder)
	}
	if validator != nil {
		input = input.Validate(validator)
	}

	return s.Run(ctx, input)
}

func (s *IOStreams) InputWithDescription(ctx context.Context, result *string, title, description, placeholder string, validator func(string) error) error {
	input := huh.NewInput().
		Title(title).
		Description(description).
		Value(result)

	if placeholder != "" {
		input = input.Placeholder(placeholder)
	}
	if validator != nil {
		input = input.Validate(validator)
	}

	return s.Run(ctx, input)
}

func (s *IOStreams) Password(ctx context.Context, result *string, title string, validator func(string) error) error {
	input := huh.NewInput().
		Title(title).
		EchoMode(huh.EchoModePassword).
		Value(result)

	if validator != nil {
		input = input.Validate(validator)
	}

	return s.Run(ctx, input)
}

func (s *IOStreams) Select(ctx context.Context, result *string, title string, options []string) error {
	return s.Run(ctx,
		huh.NewSelect[string]().
			Title(title).
			Options(huh.NewOptions(options...)...).
			Value(result))
}

func (s *IOStreams) MultiSelect(ctx context.Context, result *[]string, title string, options []string) error {
	// Set a reasonable height limit for the multiselect to ensure it displays properly
	limit := min(len(options), 10)

	return s.Run(ctx,
		huh.NewMultiSelect[string]().
			Title(title).
			Options(huh.NewOptions(options...)...).
			Filterable(true).
			Limit(limit).
			Value(result))
}

func (s *IOStreams) Multiline(ctx context.Context, result *string, title, placeholder string) error {
	text := huh.NewText().
		Title(title).
		Value(result)

	if placeholder != "" {
		text = text.Placeholder(placeholder)
	}

	return s.Run(ctx, text)
}

func (s *IOStreams) Editor(ctx context.Context, result *string, title, description, defaultContent, editorCmd string) error {
	// Set the default content if provided - MUST be done before Value(result)
	if defaultContent != "" {
		*result = defaultContent
	}

	text := huh.NewText().
		Title(title).
		Value(result).
		ExternalEditor(true).
		EditorExtension(".md")

	// Set the description (help text) if provided
	if description != "" {
		text = text.Description(description)
	}

	// Set the editor command if provided
	if editorCmd != "" {
		// Parse editor command with arguments and pass to huh's variadic Editor method
		editorParts := utils.ParseEditorCommand(editorCmd)
		if len(editorParts) > 0 {
			text = text.Editor(editorParts...)
		}
	}

	return s.Run(ctx, text)
}

// DirectEditor opens an external editor with the given content in a temporary file,
// waits for the editor to close, and returns the edited content. This is useful when
// the user explicitly requests to use an external editor (e.g., via -d- flag).
func (s *IOStreams) DirectEditor(ctx context.Context, result *string, defaultContent, editorCmd string) error {
	// Create a temporary file with the default content
	tmpFile, err := os.CreateTemp(os.TempDir(), "*.md")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Write the default content to the temp file
	if defaultContent != "" {
		if _, err := tmpFile.WriteString(defaultContent); err != nil {
			_ = tmpFile.Close() // Best effort cleanup
			return fmt.Errorf("failed to write to temp file: %w", err)
		}
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Determine which editor to use
	editor := editorCmd
	if editor == "" {
		// Fall back to environment variables in order of preference
		switch {
		case os.Getenv("VISUAL") != "":
			editor = os.Getenv("VISUAL")
		case os.Getenv("EDITOR") != "":
			editor = os.Getenv("EDITOR")
		default:
			editor = "nano"
		}
	}

	// Parse editor command and args using shlex to handle quoted arguments
	editorParts, err := shlex.Split(editor)
	if err != nil || len(editorParts) == 0 {
		return fmt.Errorf("failed to parse editor command: %w", err)
	}

	// Build command with editor args and temp file path
	cmd := exec.CommandContext(ctx, editorParts[0], append(editorParts[1:], tmpPath)...)

	// Connect the editor to the terminal
	cmd.Stdin = s.In
	cmd.Stdout = s.StdOut
	cmd.Stderr = s.StdErr

	// Run the editor
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run editor: %w", err)
	}

	// Read the edited content back
	editedContent, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to read edited content: %w", err)
	}

	*result = string(editedContent)
	return nil
}

func (s *IOStreams) Run(ctx context.Context, field huh.Field) error {
	group := huh.NewGroup(field)

	// Show help for Text and MultiSelect fields since they have non-obvious keybindings
	showHelp := false
	if _, isText := field.(*huh.Text); isText {
		showHelp = true
	}
	if _, isMultiSelect := field.(*huh.MultiSelect[string]); isMultiSelect {
		showHelp = true
	}

	form := huh.NewForm(group).
		WithShowHelp(showHelp).
		WithTheme(theme.HuhTheme())
	s.applyFormIO(form)

	err := form.RunWithContext(ctx)

	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(s.StdErr, "Cancelled.")
		return ErrUserCancelled
	}

	return err
}

// RunForm runs a multi-field form with all fields in a single group.
// This allows users to navigate between fields and provides a better UX than running
// individual prompts in sequence.
func (s *IOStreams) RunForm(ctx context.Context, fields ...huh.Field) error {
	if len(fields) == 0 {
		return nil
	}

	group := huh.NewGroup(fields...)

	showHelp := false
	for _, field := range fields {
		if _, isText := field.(*huh.Text); isText {
			showHelp = true
			break
		}
		if _, isMultiSelect := field.(*huh.MultiSelect[string]); isMultiSelect {
			showHelp = true
			break
		}
	}

	form := huh.NewForm(group).
		WithShowHelp(showHelp).
		WithTheme(theme.HuhTheme())
	s.applyFormIO(form)

	err := form.RunWithContext(ctx)

	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(s.StdErr, "Cancelled.")
		return ErrUserCancelled
	}

	return err
}

func (s *IOStreams) applyFormIO(form *huh.Form) {
	if len(s.programOptions) > 0 {
		opts := []tea.ProgramOption{
			tea.WithInput(nopCloseReader{s.In}),
			tea.WithOutput(s.StdOut),
		}
		opts = append(opts, s.programOptions...)
		form.WithProgramOptions(opts...)
	} else {
		form.WithInput(s.In).WithOutput(s.StdOut)
	}
}

type nopCloseReader struct {
	io.Reader
}

func (nopCloseReader) Close() error { return nil }
