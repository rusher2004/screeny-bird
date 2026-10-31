package derive

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rusher2004/screeny-bird/catalog"
)

// ErrNoDirectory reports a loader built without a directory.
var ErrNoDirectory = errors.New("no derived directory given")

// Index is the validated derived index: the size every image was made at and
// one entry per species.
type Index struct {
	Width, Height int
	Species       []Entry
}

// DirLoader reads the derived images and their index from one directory.
type DirLoader struct {
	dir    string
	assets catalog.DirLoader
}

// NewDirLoader returns a loader for the derived directory dir.
func NewDirLoader(dir string) (DirLoader, error) {
	if strings.TrimSpace(dir) == "" {
		return DirLoader{}, fmt.Errorf("creating loader: %w", ErrNoDirectory)
	}

	assets, err := catalog.NewDirLoader(dir)
	if err != nil {
		return DirLoader{}, fmt.Errorf("opening the directory loader: %w", err)
	}

	return DirLoader{dir: dir, assets: assets}, nil
}

// Load reads the derived index and checks that it is consistent. The index is
// read like an image: through os.Root, as a plain file under the size limit.
// It does not read the images: callers verify those against the index's
// checksums.
func (l DirLoader) Load() (Index, error) {
	raw, err := l.assets.ReadAsset(indexFile)
	if err != nil {
		return Index{}, fmt.Errorf("reading the derived index of %s: %w", l.dir, err)
	}

	idx, err := parseIndex(raw)
	if err != nil {
		return Index{}, err
	}

	return Index{Width: idx.Width, Height: idx.Height, Species: idx.Species}, nil
}

// IndexVersion returns a token that changes whenever the derived index is
// replaced, so a change can be noticed without loading. A symlink or anything
// but a plain file is refused, as it is when loading.
func (l DirLoader) IndexVersion() (version string, err error) {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return "", fmt.Errorf("opening the derived directory %s: %w", l.dir, err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing the derived directory %s: %w", l.dir, closeErr))
		}
	}()

	info, err := root.Lstat(indexFile)
	if err != nil {
		return "", fmt.Errorf("inspecting the derived index of %s: %w", l.dir, err)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("the derived index of %s: %w", l.dir, catalog.ErrNotRegularFile)
	}

	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()), nil
}

// ReadAsset returns the bytes of the plain file name inside the derived
// directory, opened through os.Root so no name can reach outside it.
func (l DirLoader) ReadAsset(name string) ([]byte, error) {
	data, err := l.assets.ReadAsset(name)
	if err != nil {
		return nil, fmt.Errorf("reading derived image: %w", err)
	}

	return data, nil
}
