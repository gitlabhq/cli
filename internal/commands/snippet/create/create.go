package create

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	title           string
	description     string
	displayFilename string
	visibility      string
	personal        bool

	files []*gitlab.CreateSnippetFileOptions

	io           *iostreams.IOStreams
	config       func() config.Config
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func (opts *options) addFile(path, content *string) {
	opts.files = append(opts.files, &gitlab.CreateSnippetFileOptions{
		FilePath: path,
		Content:  content,
	})
}

func hasStdIn() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	// Check if stdin is a pipe or redirected file (not a character device/TTY).
	// Note: fi.Size() is always 0 for pipes, so we check the mode instead.
	return (fi.Mode() & os.ModeCharDevice) == 0
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		config:       f.Config,
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	snippetCreateCmd := &cobra.Command{
		Use: `create [flags] -t <title> <file1> [<file2>...]
glab snippet create [flags] -t <title> -f <filename>  # reads from stdin`,
		Short:   `Create a new snippet.`,
		Long:    ``,
		Aliases: []string{"new"},
		Example: heredoc.Doc(`
			glab snippet create script.py --title "Title of the snippet"
			echo "package main" | glab snippet new --title "Title of the snippet" --filename "main.go"
			glab snippet create -t Title -f "different.go" -d Description main.go
			glab snippet create -t Title -f "different.go" -d Description --filename different.go main.go
			glab snippet create --personal --title "Personal snippet" script.py`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	snippetCreateCmd.Flags().StringVarP(&opts.title, "title", "t", "", "(required) Title of the snippet.")
	snippetCreateCmd.Flags().StringVarP(&opts.displayFilename, "filename", "f", "", "Filename of the snippet in GitLab.")
	snippetCreateCmd.Flags().StringVarP(&opts.description, "description", "d", "", "Description of the snippet. Set to \"-\" to open an editor.")
	snippetCreateCmd.Flags().StringVarP(&opts.visibility, "visibility", "v", "private", "Limit by visibility: 'public', 'internal', or 'private'")
	snippetCreateCmd.Flags().BoolVarP(&opts.personal, "personal", "p", false, "Create a personal snippet. (default false)")

	return snippetCreateCmd
}

func (o *options) complete(args []string) error {
	if len(args) == 0 {
		if o.displayFilename == "" {
			return &cmdutils.FlagError{Err: errors.New("if 'path' is not provided, 'filename' and stdin are required")}
		} else {
			if !o.io.IsInTTY && !hasStdIn() {
				return errors.New("stdin required if no 'path' is provided")
			}
		}
		fmt.Fprintln(o.io.StdOut, "reading from stdin (Ctrl+D to finish, Ctrl+C to abort):")
		content, err := readFromSTDIN(o.io)
		if err != nil {
			return err
		}
		o.addFile(&o.displayFilename, &content)
	} else {
		for _, path := range args {
			filename := path
			if len(args) == 1 && o.displayFilename != "" {
				filename = o.displayFilename
			}

			content, err := readFromFile(path)
			if err != nil {
				return err
			}
			dbg.Debug("Adding:", filename)
			o.addFile(&filename, &content)
		}
	}

	return nil
}

func (o *options) validate() error {
	if o.title == "" {
		return &cmdutils.FlagError{
			Err: errors.New("--title required for snippets"),
		}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	// Handle -d- flag: open editor without template selection
	err := cmdutils.HandleDescriptionEditor(ctx, &o.description, o.io, o.config, nil)
	if err != nil {
		return err
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	var repo glrepo.Interface
	if !o.personal {
		repo, err = o.baseRepo()
		if err != nil {
			redCheck := o.io.Color().FailedIcon()
			return fmt.Errorf("%s Project snippet needs a repository. Do you want --personal?", redCheck)
		}
	}

	var snippet *gitlab.Snippet
	if o.personal {
		fmt.Fprintln(o.io.StdErr, "- Creating snippet in personal space")
		snippet, _, err = client.Snippets.CreateSnippet(&gitlab.CreateSnippetOptions{
			Title:       &o.title,
			Description: &o.description,
			Visibility:  new(gitlab.VisibilityValue(o.visibility)),
			Files:       &o.files,
		})
	} else {
		fmt.Fprintln(o.io.StdErr, "- Creating snippet in", repo.FullName())
		snippet, _, err = client.ProjectSnippets.CreateSnippet(repo.FullName(), &gitlab.CreateProjectSnippetOptions{
			Title:       &o.title,
			Description: &o.description,
			Visibility:  new(gitlab.VisibilityValue(o.visibility)),
			Files:       &o.files,
		})
	}
	if err != nil {
		return fmt.Errorf("failed to create snippet: %w", err)
	}
	snippetID := o.io.Color().Green(fmt.Sprintf("$%d", snippet.ID))
	var files []string
	for _, file := range o.files {
		files = append(files, *file.FilePath)
	}
	names := strings.Join(files, " ")
	if o.io.IsaTTY {
		fmt.Fprintf(o.io.StdOut, "%s %s (%s)\n %s\n", snippetID, snippet.Title, names, snippet.WebURL)
	} else {
		fmt.Fprintln(o.io.StdOut, snippet.WebURL)
	}

	return nil
}

func readFromSTDIN(ioStream *iostreams.IOStreams) (string, error) {
	content, err := io.ReadAll(ioStream.In)
	if err != nil {
		return "", fmt.Errorf("Failed to read snippet from STDIN. %w", err)
	}
	return string(content), nil
}

func readFromFile(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("Failed to read snippet from file '%s'. %w", filename, err)
	}
	return string(content), nil
}
