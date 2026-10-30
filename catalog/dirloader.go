package catalog

import "os"

// DirLoader loads catalogs from directories on disk.
type DirLoader struct{}

// LoadDir validates the catalog in the directory dir. See Load.
func (DirLoader) LoadDir(dir string) (*Catalog, error) {
	return Load(os.DirFS(dir))
}
