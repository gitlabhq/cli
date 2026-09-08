//go:build !integration

package artifact

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func doesFileExist(fileName string) bool {
	_, err := os.Stat(fileName)
	return err == nil
}

func createZipBuffer(t *testing.T, filename string) *bytes.Reader {
	t.Helper()

	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	f1, err := os.Open("./testdata/file.txt")
	require.NoError(t, err)
	defer f1.Close()

	w1, err := zipWriter.Create(filename)
	require.NoError(t, err)

	_, err = io.Copy(w1, f1)
	require.NoError(t, err)

	err = zipWriter.Close()
	require.NoError(t, err)

	return bytes.NewReader(buf.Bytes())
}

func createSymlinkZipBuffer(t *testing.T, tempPath string) *bytes.Reader {
	t.Helper()

	buf := new(bytes.Buffer)

	immutableFile, err := os.CreateTemp(tempPath, "immutable_file*.txt")
	require.NoError(t, err)

	immutableText := "Immutable text! GitLab is cool"
	_, err = immutableFile.WriteString(immutableText)
	require.NoError(t, err)
	immutableFile.Close()

	err = os.Symlink(immutableFile.Name(), filepath.Join(tempPath, "symlink_file.txt"))
	require.NoError(t, err)

	zipWriter := zip.NewWriter(buf)

	fixtureFile, err := os.Open("./testdata/file.txt")
	require.NoError(t, err)
	defer fixtureFile.Close()

	zipFile, err := zipWriter.Create("symlink_file.txt")
	require.NoError(t, err)

	_, err = io.Copy(zipFile, fixtureFile)
	require.NoError(t, err)

	err = zipWriter.Close()
	require.NoError(t, err)

	return bytes.NewReader(buf.Bytes())
}

func Test_NewCmdRun(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
		wantErr  bool
	}{
		{
			name:     "A regular filename",
			filename: "cli-v1.22.0.json",
			want:     "cli-v1.22.0.json",
		},
		{
			name:     "A regular filename in a directory",
			filename: "cli/cli-v1.22.0.json",
			want:     "cli/cli-v1.22.0.json",
		},
		{
			name:     "A filename with directory traversal",
			filename: "cli-v1.../../22.0.zip",
			wantErr:  true,
		},
		{
			name:     "A particularly nasty filename",
			filename: "..././..././..././etc/password_file",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tempPath := t.TempDir()

			zipBuffer := createZipBuffer(t, tt.filename)

			testClient.MockJobs.EXPECT().
				DownloadArtifactsFile("OWNER/REPO", "main", gomock.Any(), gomock.Any()).
				Return(zipBuffer, nil, nil)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRun,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			_, err := exec("main secret_detection --path=" + filepath.ToSlash(tempPath))

			// THEN
			filePathWanted := filepath.Join(tempPath, tt.want)

			if tt.wantErr {
				assert.Error(t, err, "Should error out if a path doesn't exist")
				return
			}

			require.NoError(t, err, "Should not have errors")
			assert.True(t, doesFileExist(filePathWanted), "File should exist")
		})
	}

	t.Run("symlink can't overwrite", func(t *testing.T) {
		// GIVEN
		testClient := gitlabtesting.NewTestClient(t)
		tempPath := t.TempDir()

		zipBuffer := createSymlinkZipBuffer(t, tempPath)

		testClient.MockJobs.EXPECT().
			DownloadArtifactsFile("OWNER/REPO", "main", gomock.Any(), gomock.Any()).
			Return(zipBuffer, nil, nil)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmdRun,
			false,
			cmdtest.WithGitLabClient(testClient.Client),
		)

		// WHEN
		_, err := exec("main secret_detection --path=" + filepath.ToSlash(tempPath))

		// THEN
		assert.Error(t, err, "file in artifact would overwrite a symbolic link- cannot extract")
	})
}
