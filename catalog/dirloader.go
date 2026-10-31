package catalog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rusher2004/screeny-bird/fsutil"
)

var (
	// ErrNoDirectory reports a loader constructed without a directory.
	ErrNoDirectory = errors.New("catalog directory is required")
	// ErrInvalidAssetName reports an asset name that is not a clean path inside
	// the catalog directory.
	ErrInvalidAssetName = errors.New("invalid asset name")
)

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

// ReadAsset returns the bytes of the plain file name inside the loader's
// directory. name must be a clean slash-separated path inside it. The file is
// opened through os.Root, so no name can reach outside the directory, and
// symlinks, directories and files over the size limit are refused. It does not
// check that the file belongs to the catalog: callers decide which names to ask
// for and verify what they get.
func (l DirLoader) ReadAsset(name string) (data []byte, err error) {
	if name == "." || !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return nil, fmt.Errorf("reading asset %q: %w", name, ErrInvalidAssetName)
	}

	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, fmt.Errorf("opening catalog directory %s: %w", l.dir, err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing catalog directory %s: %w", l.dir, closeErr))
		}
	}()

	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("reading asset %q: %w", name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("asset %q: %w", name, ErrNotRegularFile)
	}

	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("asset %q is %d bytes, over the %d byte limit: %w", name, info.Size(), maxFileBytes, errTooLarge)
	}

	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("opening asset %q: %w", name, err)
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing asset %q: %w", name, closeErr))
		}
	}()

	data, err = io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading asset %q: %w", name, err)
	}

	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("asset %q grew past the %d byte limit: %w", name, maxFileBytes, errTooLarge)
	}

	return data, nil
}

// IndexVersion returns a token that changes whenever index.json is replaced,
// so a caller can tell cheaply that the catalog may have changed without
// loading it.
func (l DirLoader) IndexVersion() (string, error) {
	info, err := os.Stat(filepath.Join(l.dir, indexFile))
	if err != nil {
		return "", fmt.Errorf("inspecting the index of %s: %w", l.dir, err)
	}

	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()), nil
}
