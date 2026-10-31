// Package fsutil holds the small filesystem helpers the service needs: atomic
// file writes and an advisory process lock.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path so that a reader sees either the old
// content or the new content, never a partial file. It writes a temporary file
// in the same directory, syncs it and renames it over path. On failure the
// temporary file is removed and any existing file is untouched.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary file for %s: %w", path, err)
	}

	tmpPath := tmp.Name()
	closed := false

	defer func() {
		if err == nil {
			return
		}

		if !closed {
			if closeErr := tmp.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("closing temporary file %s: %w", tmpPath, closeErr))
			}
		}

		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing temporary file %s: %w", tmpPath, removeErr))
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("writing temporary file %s: %w", tmpPath, err)
	}

	if err = tmp.Chmod(perm); err != nil {
		return fmt.Errorf("setting mode of temporary file %s: %w", tmpPath, err)
	}

	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temporary file %s: %w", tmpPath, err)
	}

	closed = true

	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary file %s: %w", tmpPath, err)
	}

	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}

	return nil
}
