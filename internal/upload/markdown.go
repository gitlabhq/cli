// Package upload uploads local files to a project and returns the markdown
// references that embed them in an issue, merge request, or note.
package upload

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const StdinPath = "-"

const stdinName = "upload"

// sniffLen matches the window http.DetectContentType reads.
const sniffLen = 512

// imageExtensions maps the sniffable content types GitLab renders inline to the
// extension its filename needs to carry. mime.ExtensionsByType is unusable
// here because its result is sorted: image/jpeg yields ".jfif" first.
var imageExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Attachment is a local file to upload and reference from a markdown body.
type Attachment struct {
	Name string
	Open func() (io.ReadCloser, error)
}

// Attachments resolves attachment paths, reading stdin for [StdinPath]. It
// fails before any upload starts, so a bad path leaves nothing uploaded.
func Attachments(stdin io.Reader, paths []string) ([]Attachment, error) {
	attachments := make([]Attachment, 0, len(paths))
	stdinUsed := false

	for _, path := range paths {
		if path != StdinPath {
			attachment, err := fileAttachment(path)
			if err != nil {
				return nil, err
			}
			attachments = append(attachments, attachment)
			continue
		}

		if stdinUsed {
			return nil, fmt.Errorf("attachment %q can only be given once", StdinPath)
		}
		stdinUsed = true

		attachment, err := stdinAttachment(stdin)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}

	return attachments, nil
}

func fileAttachment(path string) (Attachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, fmt.Errorf("failed to read attachment: %w", err)
	}
	if info.IsDir() {
		return Attachment{}, fmt.Errorf("attachment %q is a directory", path)
	}

	return Attachment{
		Name: filepath.Base(path),
		Open: func() (io.ReadCloser, error) { return os.Open(path) },
	}, nil
}

func stdinAttachment(stdin io.Reader) (Attachment, error) {
	buffered := bufio.NewReaderSize(stdin, sniffLen)

	head, err := buffered.Peek(sniffLen)
	if err != nil && !errors.Is(err, io.EOF) {
		return Attachment{}, fmt.Errorf("failed to read attachment from standard input: %w", err)
	}
	if len(head) == 0 {
		return Attachment{}, errors.New("attachment on standard input is empty")
	}

	return Attachment{
		Name: stdinName + sniffExtension(head),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(buffered), nil },
	}, nil
}

// sniffExtension returns the extension GitLab needs to render head inline.
func sniffExtension(head []byte) string {
	contentType, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		return ""
	}
	return imageExtensions[contentType]
}

// Upload uploads each attachment and returns a markdown reference to each, in
// order. On failure the error names the ones that did upload, so a retry can
// reuse them.
func Upload(ctx context.Context, ios *iostreams.IOStreams, client *gitlab.Client, projectPath string, attachments []Attachment) ([]string, error) {
	color := ios.Color()
	references := make([]string, 0, len(attachments))

	for _, attachment := range attachments {
		// Progress goes to stderr so it cannot corrupt piped command output.
		ios.LogErrorf("%s Uploading %s\n", color.ProgressIcon(), color.Blue(attachment.Name))

		reference, err := uploadOne(ctx, client, projectPath, attachment)
		if err != nil {
			return nil, uploadError(attachment.Name, err, references)
		}
		references = append(references, reference)
	}

	return references, nil
}

func uploadOne(ctx context.Context, client *gitlab.Client, projectPath string, attachment Attachment) (_ string, err error) {
	content, err := attachment.Open()
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, content.Close()) }()

	uploaded, _, err := client.ProjectMarkdownUploads.UploadProjectMarkdown(
		projectPath,
		content,
		attachment.Name,
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return "", err
	}

	return uploaded.Markdown, nil
}

func uploadError(name string, err error, uploaded []string) error {
	wrapped := fmt.Errorf("failed to upload %s: %w", name, err)
	if len(uploaded) == 0 {
		return wrapped
	}
	return fmt.Errorf("%w\n\nAlready uploaded, reusable without uploading again:\n%s", wrapped, strings.Join(uploaded, "\n"))
}

// Append returns body with references appended after a blank line.
func Append(body string, references []string) string {
	if len(references) == 0 {
		return body
	}

	joined := strings.Join(references, "\n")
	if strings.TrimSpace(body) == "" {
		return joined
	}
	return strings.TrimRight(body, "\n") + "\n\n" + joined
}
