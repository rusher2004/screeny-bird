// Package catalog reads and validates the approved bird plate catalog.
//
// A catalog is a directory tree: an index.json and, under species/, one
// directory per species holding a manifest, a portrait, a display image and
// optional companion JSON. DirLoader validates the whole tree and refuses to
// return a partial result.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

const (
	indexFile     = "index.json"
	speciesRoot   = "species"
	schemaVersion = 1

	// maxFileBytes bounds every file read from a catalog, because catalog
	// content is untrusted. It is a safety invariant, not an operator setting.
	maxFileBytes = 32 << 20
)

// The sentinel errors let callers branch on the rule a catalog broke.
var (
	// ErrUnsupportedSchema reports a manifest or index schema version this
	// package does not understand.
	ErrUnsupportedSchema = errors.New("unsupported schema version")
	// ErrInvalidManifest reports a manifest that is malformed or has fields of
	// the wrong type or value.
	ErrInvalidManifest = errors.New("invalid manifest")
	// ErrIdentityMismatch reports a manifest whose status, taxon ID or slug does
	// not agree with its directory.
	ErrIdentityMismatch = errors.New("manifest identity does not match its directory")
	// ErrUnexpectedFile reports a file or directory that a catalog must not contain.
	ErrUnexpectedFile = errors.New("unexpected catalog entry")
	// ErrMissingFile reports a required catalog file or directory that is absent.
	ErrMissingFile = errors.New("required catalog entry is missing")
	// ErrNotRegularFile reports a symlink or other irregular entry where a
	// plain file or directory is required.
	ErrNotRegularFile = errors.New("catalog entry is not a plain file or directory")
	// ErrChecksumMismatch reports an asset whose SHA-256 differs from its manifest.
	ErrChecksumMismatch = errors.New("asset checksum mismatch")
	// ErrReviewNotPublishable reports a quality review or generation record
	// that does not meet the publication gates.
	ErrReviewNotPublishable = errors.New("manifest lacks a publishable quality review")
	// ErrPrivateField reports a field that must never appear in a public catalog.
	ErrPrivateField = errors.New("private field in catalog JSON")
	// ErrLocalPath reports a local filesystem path inside catalog JSON.
	ErrLocalPath = errors.New("local path in catalog JSON")
	// ErrProfileMismatch reports a companion JSON file that differs from the manifest.
	ErrProfileMismatch = errors.New("companion file does not match the manifest")
	// ErrInvalidImage reports an image that is not an acceptable PNG.
	ErrInvalidImage = errors.New("invalid catalog image")
	// ErrDuplicateTaxon reports two species directories with the same taxon ID.
	ErrDuplicateTaxon = errors.New("duplicate taxon ID")
	// ErrIndexMismatch reports an index.json that is malformed or does not
	// match the species manifests.
	ErrIndexMismatch = errors.New("index does not match the species manifests")
)

// SpeciesError attributes a validation failure to one species directory.
type SpeciesError struct {
	Dir string
	Err error
}

func (e *SpeciesError) Error() string {
	return fmt.Sprintf("species %s: %v", e.Dir, e.Err)
}

func (e *SpeciesError) Unwrap() error {
	return e.Err
}

// Species is one approved plate as the catalog index lists it. The field order
// is the sorted JSON key order.
type Species struct {
	ApprovedAt     string `json:"approved_at"`
	CommonName     string `json:"common_name"`
	DisplayPath    string `json:"display_path"`
	DisplaySHA256  string `json:"display_sha256"`
	PortraitPath   string `json:"portrait_path"`
	PortraitSHA256 string `json:"portrait_sha256"`
	ScientificName string `json:"scientific_name"`
	Slug           string `json:"slug"`
	TaxonID        int    `json:"taxon_id"`
}

// Catalog is a fully validated catalog.
type Catalog struct {
	species []Species
}

// Species returns the catalog's species sorted by case-folded common name,
// then taxon ID.
func (c *Catalog) Species() []Species {
	return slices.Clone(c.species)
}

// GeneratedAt returns the latest approval time among the species, and false
// for an empty catalog.
func (c *Catalog) GeneratedAt() (string, bool) {
	latest := indexOf(c.species).GeneratedAt
	if latest == nil {
		return "", false
	}

	return *latest, true
}

// load validates every part of the catalog rooted at fsys, including that
// index.json matches the species, and returns it. When anything is wrong it
// returns no catalog and an error joining every problem found, each species
// problem wrapped in a *SpeciesError.
func load(fsys fs.FS) (*Catalog, error) {
	species, err := loadTree(fsys, true)
	if err != nil {
		return nil, err
	}

	if err := checkIndex(fsys, indexOf(species)); err != nil {
		return nil, fmt.Errorf("checking %s: %w", indexFile, err)
	}

	return &Catalog{species: species}, nil
}

// loadTree validates the catalog root and every species and returns the
// species sorted. It does not check index.json's content; requireIndex says
// whether the file must exist.
func loadTree(fsys fs.FS, requireIndex bool) ([]Species, error) {
	var errs []error

	if err := checkRoot(fsys, requireIndex); err != nil {
		errs = append(errs, err)
	}

	// A missing or unreadable species root yields no directories and an error;
	// problems with individual entries still leave the valid ones to check.
	dirs, err := speciesDirs(fsys)
	if err != nil {
		errs = append(errs, err)
	}

	var (
		species []Species
		seen    = map[int]string{}
	)

	for _, dir := range dirs {
		s, err := loadSpecies(fsys, dir)
		if err != nil {
			errs = append(errs, &SpeciesError{Dir: dir, Err: err})

			continue
		}

		if first, dup := seen[s.TaxonID]; dup {
			errs = append(errs, &SpeciesError{
				Dir: dir,
				Err: fmt.Errorf("taxon %d is already used by %s: %w", s.TaxonID, first, ErrDuplicateTaxon),
			})

			continue
		}

		seen[s.TaxonID] = dir
		species = append(species, s)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	sortSpecies(species)

	return species, nil
}

func sortSpecies(species []Species) {
	slices.SortFunc(species, func(a, b Species) int {
		if c := strings.Compare(strings.ToLower(a.CommonName), strings.ToLower(b.CommonName)); c != 0 {
			return c
		}

		return a.TaxonID - b.TaxonID
	})
}

// checkRoot confirms the catalog root holds only the index and species root,
// and that index.json, when it must exist or does, is a plain file.
func checkRoot(fsys fs.FS, requireIndex bool) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("reading catalog root: %w: %w", ErrMissingFile, err)
	}

	var errs []error

	for _, entry := range entries {
		if name := entry.Name(); name != indexFile && name != speciesRoot {
			errs = append(errs, fmt.Errorf("catalog root entry %q: %w", name, ErrUnexpectedFile))
		}
	}

	info, err := fs.Lstat(fsys, indexFile)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		if requireIndex {
			errs = append(errs, fmt.Errorf("%s: %w", indexFile, ErrMissingFile))
		}
	case err != nil:
		errs = append(errs, fmt.Errorf("inspecting %s: %w", indexFile, err))
	case !info.Mode().IsRegular():
		errs = append(errs, fmt.Errorf("%s: %w", indexFile, ErrNotRegularFile))
	}

	return errors.Join(errs...)
}

// speciesDirs lists the species directory names in sorted order.
func speciesDirs(fsys fs.FS) ([]string, error) {
	info, err := fs.Lstat(fsys, speciesRoot)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s: %w", speciesRoot, ErrMissingFile)
	case err != nil:
		return nil, fmt.Errorf("inspecting %s: %w", speciesRoot, err)
	case !info.IsDir():
		return nil, fmt.Errorf("%s: %w", speciesRoot, ErrNotRegularFile)
	}

	entries, err := fs.ReadDir(fsys, speciesRoot)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", speciesRoot, err)
	}

	var (
		dirs []string
		errs []error
	)

	for _, entry := range entries {
		name := entry.Name()

		// macOS Finder metadata is the one stray file tolerated here.
		if name == ".DS_Store" {
			continue
		}

		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			errs = append(errs, &SpeciesError{Dir: name, Err: ErrNotRegularFile})

			continue
		}

		dirs = append(dirs, name)
	}

	return dirs, errors.Join(errs...)
}

// readFile reads a regular file from the catalog, refusing symlinks and
// oversized files.
func readFile(fsys fs.FS, name string) ([]byte, error) {
	info, err := fs.Lstat(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", name, ErrNotRegularFile)
	}

	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit: %w", name, info.Size(), maxFileBytes, errTooLarge)
	}

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	return data, nil
}

var errTooLarge = errors.New("file too large")

func speciesPath(dir, file string) string {
	return path.Join(speciesRoot, dir, file)
}
