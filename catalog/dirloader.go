package catalog

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNoDirectory reports a loader constructed without a directory.
var ErrNoDirectory = errors.New("catalog directory is required")

// DirLoader loads the catalog kept in one directory on disk.
type DirLoader struct {
	dir string
}

// NewDirLoader returns a loader for the catalog in dir. A blank dir is an error.
func NewDirLoader(dir string) (DirLoader, error) {
	if strings.TrimSpace(dir) == "" {
		return DirLoader{}, fmt.Errorf("creating directory loader: %w", ErrNoDirectory)
	}

	return DirLoader{dir: dir}, nil
}

// Load validates the catalog in the loader's directory. See the package
// function Load.
func (l DirLoader) Load() (*Catalog, error) {
	return Load(os.DirFS(l.dir))
}
