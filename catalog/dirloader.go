package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rusher2004/screeny-bird/fsutil"
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

// Load validates every part of the catalog in the loader's directory and
// returns it. When anything is wrong it returns no catalog and an error joining
// every problem found, each species problem wrapped in a *SpeciesError.
func (l DirLoader) Load() (*Catalog, error) {
	return load(os.DirFS(l.dir))
}

// RebuildIndex validates the root and every species in the loader's directory,
// then atomically writes the canonical index.json for them, replacing any
// existing one. The index is not checked or changed unless the whole tree is
// valid. It returns the catalog the new index describes.
func (l DirLoader) RebuildIndex() (*Catalog, error) {
	species, err := loadTree(os.DirFS(l.dir), false)
	if err != nil {
		return nil, err
	}

	data, err := indexOf(species).marshal()
	if err != nil {
		return nil, fmt.Errorf("rebuilding the index of %s: %w", l.dir, err)
	}

	if err := fsutil.WriteFileAtomic(filepath.Join(l.dir, indexFile), data, 0o644); err != nil {
		return nil, fmt.Errorf("writing the index of %s: %w", l.dir, err)
	}

	return &Catalog{species: species}, nil
}
