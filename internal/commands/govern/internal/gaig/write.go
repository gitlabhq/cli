package gaig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces the file at path with data, readable only by its
// owner. It writes a temporary file in the same directory and renames it into
// place, on every operating system, so that a sync running at the same time,
// or a write cut short, never leaves a partly written file.
func WriteFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if rmErr := os.Remove(tmp.Name()); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = errors.Join(err, rmErr)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
