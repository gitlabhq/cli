//go:build !integration

package artifact

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

const numTestFiles = 100

func createTestZipFile(t *testing.T) string {
	t.Helper()

	tempFile, err := os.CreateTemp(t.TempDir(), "temp-*.zip")
	if err != nil {
		t.Fatalf("Create temp file: %v", err)
	}

	zipWriter := zip.NewWriter(tempFile)

	for i := range numTestFiles {
		fileName := "file-" + strconv.Itoa(i) + ".txt"

		fileWriter, err := zipWriter.Create(fileName)
		if err != nil {
			t.Fatalf("Create zip: %v", err)
		}

		_, err = fileWriter.Write([]byte(fileName))
		if err != nil {
			t.Fatalf("Write zip: %v", err)
		}
	}

	if err := zipWriter.Close(); err != nil {
		t.Fatalf("Close zip: %v", err)
	}

	if err := tempFile.Close(); err != nil {
		t.Fatalf("Close temp: %v", err)
	}

	return tempFile.Name()
}

func toByteReader(zipFilePath string) (*bytes.Reader, error) {
	zipBytes, err := os.ReadFile(zipFilePath)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(zipBytes), nil
}

func listFilesInDir(dirPath string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}

func TestAcceptableZipFile(t *testing.T) {
	zipName := createTestZipFile(t)

	reader, err := toByteReader(zipName)
	require.NoError(t, err)

	targetDir := t.TempDir()

	var buf bytes.Buffer
	listPaths := true
	err = readZip(reader, targetDir, listPaths, defaultZIPReadLimit, defaultZIPFileLimit, &buf)
	stdout := buf.String()
	require.NoError(t, err)

	files, err := listFilesInDir(targetDir)
	require.NoError(t, err)
	require.Len(t, files, numTestFiles)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	for _, file := range files {
		path := filepath.Join(targetDir, file)
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, file, string(content))

		// friendlyPath returns a relative path when possible; fall back to absolute.
		expectedPath := path
		if rel, err := filepath.Rel(cwd, path); err == nil {
			expectedPath = rel
		}
		require.Contains(t, stdout, expectedPath)
	}
}

func TestFileLimitExceeded(t *testing.T) {
	zipName := createTestZipFile(t)

	reader, err := toByteReader(zipName)
	require.NoError(t, err)

	err = readZip(reader, t.TempDir(), false, defaultZIPReadLimit, 50, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zip archive includes too many files")
}

func TestReadLimitExceeded(t *testing.T) {
	zipName := createTestZipFile(t)

	reader, err := toByteReader(zipName)
	require.NoError(t, err)

	err = readZip(reader, t.TempDir(), false, 50, defaultZIPFileLimit, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "extracted zip too large")
}

func TestReadZipDropsSpecialModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits are not applied on Windows")
	}

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	dirHeader := &zip.FileHeader{Name: "dir/"}
	dirHeader.SetMode(os.ModeDir | os.ModeSticky | 0o755)
	_, err := zipWriter.CreateHeader(dirHeader)
	require.NoError(t, err)

	fileHeader := &zip.FileHeader{Name: "dir/bin", Method: zip.Store}
	fileHeader.SetMode(os.ModeSetuid | os.ModeSetgid | 0o755)
	w, err := zipWriter.CreateHeader(fileHeader)
	require.NoError(t, err)
	_, err = w.Write([]byte("content"))
	require.NoError(t, err)

	require.NoError(t, zipWriter.Close())

	targetDir := t.TempDir()
	err = readZip(bytes.NewReader(buf.Bytes()), targetDir, false, defaultZIPReadLimit, defaultZIPFileLimit, io.Discard)
	require.NoError(t, err)

	special := os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	for _, name := range []string{"dir", filepath.Join("dir", "bin")} {
		info, err := os.Stat(filepath.Join(targetDir, name))
		require.NoError(t, err)
		require.Zero(t, info.Mode()&special, "%s has special mode bits: %s", name, info.Mode())
	}
}

func TestSafeMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     os.FileMode
		fallback os.FileMode
		want     os.FileMode
	}{
		{"plain file", 0o644, 0o600, 0o644},
		{"setuid file", os.ModeSetuid | 0o755, 0o644, 0o755},
		{"setgid sticky dir", os.ModeDir | os.ModeSetgid | os.ModeSticky | 0o775, 0o755, 0o775},
		{"no perm bits", os.ModeSetuid, 0o644, 0o644},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, safeMode(tc.mode, tc.fallback))
		})
	}
}
