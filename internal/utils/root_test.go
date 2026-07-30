package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDestinationRoot(t *testing.T) {
	t.Run("absolute path creates missing parents and is addressed by base name", func(t *testing.T) {
		base := t.TempDir()
		path := filepath.Join(base, "a", "b", "file.zip")

		root, name, err := EnsureDestinationRoot(path)
		require.NoError(t, err)
		defer root.Close()

		assert.Equal(t, "file.zip", name)
		assert.DirExists(t, filepath.Join(base, "a", "b"))

		// The returned name must address the file through the returned root.
		// filepath.Dir of an absolute path is itself absolute, and os.Root
		// rejects absolute names, so a full path here would fail.
		f, err := root.Create(name)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		assert.FileExists(t, path)
	})

	t.Run("relative path is created under the working directory", func(t *testing.T) {
		wd := t.TempDir()
		t.Chdir(wd)

		root, name, err := EnsureDestinationRoot(filepath.Join("downloads", "nested", "file.zip"))
		require.NoError(t, err)
		defer root.Close()

		assert.Equal(t, "file.zip", name)
		assert.DirExists(t, filepath.Join(wd, "downloads", "nested"))
	})

	t.Run("bare file name resolves to the working directory", func(t *testing.T) {
		wd := t.TempDir()
		t.Chdir(wd)

		root, name, err := EnsureDestinationRoot("file.zip")
		require.NoError(t, err)
		defer root.Close()

		assert.Equal(t, "file.zip", name)

		f, err := root.Create(name)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		assert.FileExists(t, filepath.Join(wd, "file.zip"))
	})

	t.Run("relative path cannot escape the working directory", func(t *testing.T) {
		parent := t.TempDir()
		wd := filepath.Join(parent, "work")
		require.NoError(t, os.Mkdir(wd, 0o755))
		t.Chdir(wd)

		_, _, err := EnsureDestinationRoot(filepath.Join("..", "escaped", "file.zip"))
		require.Error(t, err)
		assert.NoDirExists(t, filepath.Join(parent, "escaped"))
	})

	t.Run("existing directory is reused rather than failing", func(t *testing.T) {
		base := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(base, "a"), 0o755))

		root, name, err := EnsureDestinationRoot(filepath.Join(base, "a", "file.zip"))
		require.NoError(t, err)
		defer root.Close()

		assert.Equal(t, "file.zip", name)
	})
}
