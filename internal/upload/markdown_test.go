package upload

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

var (
	pngContent  = []byte("\x89PNG\r\n\x1a\nfake png body")
	jpegContent = []byte("\xff\xd8\xff\xe0fake jpeg body")
)

func testIOStreams() *iostreams.IOStreams {
	return iostreams.New(
		iostreams.WithStdin(io.NopCloser(&bytes.Buffer{}), false),
		iostreams.WithStdout(io.Discard, false),
		iostreams.WithStderr(io.Discard, false),
	)
}

func writeTempFile(t *testing.T, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

func TestAttachments_File(t *testing.T) {
	t.Parallel()

	path := writeTempFile(t, "diagram.png", pngContent)

	attachments, err := Attachments(nil, []string{path})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, "diagram.png", attachments[0].Name)

	content, err := attachments[0].Open()
	require.NoError(t, err)
	defer content.Close()

	got, err := io.ReadAll(content)
	require.NoError(t, err)
	assert.Equal(t, pngContent, got)
}

func TestAttachments_MultipleFilesKeepOrder(t *testing.T) {
	t.Parallel()

	first := writeTempFile(t, "first.png", pngContent)
	second := writeTempFile(t, "second.jpg", jpegContent)

	attachments, err := Attachments(nil, []string{first, second})
	require.NoError(t, err)
	require.Len(t, attachments, 2)
	assert.Equal(t, "first.png", attachments[0].Name)
	assert.Equal(t, "second.jpg", attachments[1].Name)
}

func TestAttachments_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := Attachments(nil, []string{filepath.Join(t.TempDir(), "absent.png")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read attachment")
}

func TestAttachments_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := Attachments(nil, []string{dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
}

func TestAttachments_StdinSniffsPNGExtension(t *testing.T) {
	t.Parallel()

	attachments, err := Attachments(bytes.NewReader(pngContent), []string{StdinPath})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, "upload.png", attachments[0].Name)
}

func TestAttachments_StdinSniffsJPEGExtension(t *testing.T) {
	t.Parallel()

	attachments, err := Attachments(bytes.NewReader(jpegContent), []string{StdinPath})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	// Not ".jfif", which is what mime.ExtensionsByType would return first.
	assert.Equal(t, "upload.jpg", attachments[0].Name)
}

func TestAttachments_StdinUnrecognizedContentHasNoExtension(t *testing.T) {
	t.Parallel()

	attachments, err := Attachments(bytes.NewReader([]byte("just some notes")), []string{StdinPath})
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	assert.Equal(t, "upload", attachments[0].Name)
}

func TestAttachments_StdinReadsFullContentAfterSniffing(t *testing.T) {
	t.Parallel()

	// Longer than the sniff window, to prove Peek did not consume the stream.
	content := append(bytes.Clone(pngContent), bytes.Repeat([]byte("x"), sniffLen*3)...)

	attachments, err := Attachments(bytes.NewReader(content), []string{StdinPath})
	require.NoError(t, err)
	require.Len(t, attachments, 1)

	body, err := attachments[0].Open()
	require.NoError(t, err)
	defer body.Close()

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestAttachments_StdinEmpty(t *testing.T) {
	t.Parallel()

	_, err := Attachments(bytes.NewReader(nil), []string{StdinPath})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestAttachments_StdinGivenTwice(t *testing.T) {
	t.Parallel()

	_, err := Attachments(bytes.NewReader(pngContent), []string{StdinPath, StdinPath})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can only be given once")
}

func TestAttachments_NoPaths(t *testing.T) {
	t.Parallel()

	attachments, err := Attachments(nil, nil)
	require.NoError(t, err)
	assert.Empty(t, attachments)
}

func TestUpload_ReturnsReferencesInOrder(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("group/project", gomock.Any(), "first.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![first](/uploads/aaa/first.png)"}, nil, nil)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("group/project", gomock.Any(), "second.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![second](/uploads/bbb/second.png)"}, nil, nil)

	references, err := Upload(t.Context(), testIOStreams(), tc.Client, "group/project", []Attachment{
		staticAttachment("first.png", pngContent),
		staticAttachment("second.png", pngContent),
	})
	require.NoError(t, err)
	require.Len(t, references, 2)
	assert.Equal(t, "![first](/uploads/aaa/first.png)", references[0])
	assert.Equal(t, "![second](/uploads/bbb/second.png)", references[1])
}

func TestUpload_FailureNamesAlreadyUploadedFiles(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("group/project", gomock.Any(), "first.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![first](/uploads/aaa/first.png)"}, nil, nil)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("group/project", gomock.Any(), "second.png", gomock.Any()).
		Return(nil, nil, errors.New("413 Payload Too Large"))

	_, err := Upload(t.Context(), testIOStreams(), tc.Client, "group/project", []Attachment{
		staticAttachment("first.png", pngContent),
		staticAttachment("second.png", pngContent),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload second.png")
	assert.Contains(t, err.Error(), "413 Payload Too Large")
	assert.Contains(t, err.Error(), "![first](/uploads/aaa/first.png)")
}

func TestUpload_FirstFailureHasNothingToReuse(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("group/project", gomock.Any(), "only.png", gomock.Any()).
		Return(nil, nil, errors.New("403 Forbidden"))

	_, err := Upload(t.Context(), testIOStreams(), tc.Client, "group/project", []Attachment{
		staticAttachment("only.png", pngContent),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload only.png")
	assert.NotContains(t, err.Error(), "Already uploaded")
}

func TestAppend_EmptyBodyUsesReferencesAlone(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "![a](/uploads/a)", Append("", []string{"![a](/uploads/a)"}))
	assert.Equal(t, "![a](/uploads/a)", Append("   \n ", []string{"![a](/uploads/a)"}))
}

func TestAppend_SeparatesFromExistingBody(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Look at this.\n\n![a](/uploads/a)", Append("Look at this.", []string{"![a](/uploads/a)"}))
}

func TestAppend_CollapsesTrailingNewlines(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Look at this.\n\n![a](/uploads/a)", Append("Look at this.\n\n\n", []string{"![a](/uploads/a)"}))
}

func TestAppend_MultipleReferencesOnSeparateLines(t *testing.T) {
	t.Parallel()

	got := Append("Body.", []string{"![a](/uploads/a)", "![b](/uploads/b)"})
	assert.Equal(t, "Body.\n\n![a](/uploads/a)\n![b](/uploads/b)", got)
}

func TestAppend_NoReferencesLeavesBodyAlone(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Body.\n\n", Append("Body.\n\n", nil))
}

func staticAttachment(name string, content []byte) Attachment {
	return Attachment{
		Name: name,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(content)), nil },
	}
}
