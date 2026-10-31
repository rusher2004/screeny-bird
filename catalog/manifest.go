package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const (
	manifestFile = "manifest.json"
	portraitFile = "portrait.png"
	displayFile  = "display.png"
	profileFile  = "profile.json"
	reviewFile   = "quality-review.json"
)

// Image geometry is fixed by the catalog format.
const (
	portraitWidth, portraitHeight = 1200, 1600
	displayWidth, displayHeight   = 1600, 1200
)

var (
	requiredSpeciesFiles = []string{manifestFile, portraitFile, displayFile}
	allowedSpeciesFiles  = []string{manifestFile, portraitFile, displayFile, profileFile, reviewFile}
)

// manifest is the part of a species manifest this package validates. Other
// fields are preserved only through the generic decode used for privacy checks.
type manifest struct {
	SchemaVersion  int                 `json:"schema_version"`
	Status         string              `json:"status"`
	TaxonID        int                 `json:"taxon_id"`
	CommonName     string              `json:"common_name"`
	ScientificName string              `json:"scientific_name"`
	Slug           string              `json:"slug"`
	ApprovedAt     string              `json:"approved_at"`
	Generation     generation          `json:"generation"`
	QualityReview  review              `json:"quality_review"`
	Assets         map[string]assetRef `json:"assets"`
}

type assetRef struct {
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

// loadSpecies validates one species directory and returns its index entry.
// ValidSlug reports whether s is a species slug: lowercase letters and digits
// in groups joined by single hyphens.
func ValidSlug(s string) bool {
	return slugPattern.MatchString(s)
}

// ValidSHA256 reports whether s is a SHA-256 digest in lowercase hex.
func ValidSHA256(s string) bool {
	return sha256Pattern.MatchString(s)
}

func loadSpecies(fsys fs.FS, dir string) (Species, error) {
	if err := checkSpeciesFiles(fsys, dir); err != nil {
		return Species{}, err
	}

	raw, err := readFile(fsys, speciesPath(dir, manifestFile))
	if err != nil {
		return Species{}, withTooLarge(err, ErrInvalidManifest)
	}

	m, generic, err := decodeManifest(raw)
	if err != nil {
		return Species{}, err
	}

	if err := checkIdentity(m, dir); err != nil {
		return Species{}, err
	}

	if err := publishable(m.QualityReview, m.Generation); err != nil {
		return Species{}, err
	}

	if err := checkCompanions(fsys, dir, generic); err != nil {
		return Species{}, err
	}

	portraitSHA, err := checkAsset(fsys, dir, m, "portrait", portraitFile, portraitWidth, portraitHeight)
	if err != nil {
		return Species{}, err
	}

	displaySHA, err := checkAsset(fsys, dir, m, "display", displayFile, displayWidth, displayHeight)
	if err != nil {
		return Species{}, err
	}

	return Species{
		ApprovedAt:     m.ApprovedAt,
		CommonName:     m.CommonName,
		DisplayPath:    speciesPath(dir, displayFile),
		DisplaySHA256:  displaySHA,
		PortraitPath:   speciesPath(dir, portraitFile),
		PortraitSHA256: portraitSHA,
		ScientificName: m.ScientificName,
		Slug:           m.Slug,
		TaxonID:        m.TaxonID,
	}, nil
}

// withTooLarge reclassifies the oversize error under the caller's sentinel.
func withTooLarge(err, sentinel error) error {
	if errors.Is(err, errTooLarge) {
		return fmt.Errorf("%w: %w", sentinel, err)
	}

	return err
}

func checkSpeciesFiles(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, speciesPath(dir, "."))
	if err != nil {
		return fmt.Errorf("reading species directory: %w", err)
	}

	var (
		present = map[string]bool{}
		errs    []error
	)

	for _, entry := range entries {
		name := entry.Name()
		present[name] = true

		switch {
		case !slices.Contains(allowedSpeciesFiles, name):
			errs = append(errs, fmt.Errorf("file %q: %w", name, ErrUnexpectedFile))
		case !entry.Type().IsRegular():
			errs = append(errs, fmt.Errorf("file %q: %w", name, ErrNotRegularFile))
		}
	}

	for _, name := range requiredSpeciesFiles {
		if !present[name] {
			errs = append(errs, fmt.Errorf("file %q: %w", name, ErrMissingFile))
		}
	}

	return errors.Join(errs...)
}

// decodeManifest parses a manifest twice: into the typed struct the gates
// use, and into a generic value for the privacy and companion-file checks.
func decodeManifest(raw []byte) (manifest, map[string]any, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}

	if err := json.Unmarshal(raw, &header); err != nil {
		return manifest{}, nil, fmt.Errorf("decoding manifest: %w: %w", ErrInvalidManifest, err)
	}

	if header.SchemaVersion != schemaVersion {
		return manifest{}, nil, fmt.Errorf("manifest schema_version %d: %w", header.SchemaVersion, ErrUnsupportedSchema)
	}

	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}, nil, fmt.Errorf("decoding manifest: %w: %w", ErrInvalidManifest, err)
	}

	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return manifest{}, nil, fmt.Errorf("decoding manifest: %w: %w", ErrInvalidManifest, err)
	}

	return m, generic, nil
}

func checkIdentity(m manifest, dir string) error {
	switch {
	case m.Status != "approved":
		return fmt.Errorf("manifest status %q is not approved: %w", m.Status, ErrIdentityMismatch)
	case m.TaxonID <= 0 || !slugPattern.MatchString(m.Slug) || dir != fmt.Sprintf("%d-%s", m.TaxonID, m.Slug):
		return fmt.Errorf("manifest taxon %d and slug %q do not name the directory: %w", m.TaxonID, m.Slug, ErrIdentityMismatch)
	case strings.TrimSpace(m.CommonName) == "" || strings.TrimSpace(m.ScientificName) == "":
		return fmt.Errorf("manifest names are required: %w", ErrInvalidManifest)
	}

	if _, err := time.Parse(time.RFC3339, m.ApprovedAt); err != nil {
		return fmt.Errorf("manifest approved_at %q: %w: %w", m.ApprovedAt, ErrInvalidManifest, err)
	}

	return nil
}

// checkCompanions privacy-scans every JSON file in the directory and confirms
// that profile.json and quality-review.json agree with the manifest.
func checkCompanions(fsys fs.FS, dir string, manifestJSON map[string]any) error {
	if err := scanPrivacy(manifestJSON, manifestFile); err != nil {
		return err
	}

	for _, companion := range []struct{ file, key string }{
		{profileFile, "profile"},
		{reviewFile, "quality_review"},
	} {
		file, key := companion.file, companion.key
		name := speciesPath(dir, file)

		if _, err := fs.Lstat(fsys, name); errors.Is(err, fs.ErrNotExist) {
			continue
		}

		raw, err := readFile(fsys, name)
		if err != nil {
			return withTooLarge(err, ErrInvalidManifest)
		}

		var companion any
		if err := json.Unmarshal(raw, &companion); err != nil {
			return fmt.Errorf("decoding %s: %w: %w", file, ErrInvalidManifest, err)
		}

		if err := scanPrivacy(companion, file); err != nil {
			return err
		}

		if !reflect.DeepEqual(companion, manifestJSON[key]) {
			return fmt.Errorf("%s differs from the manifest's %s: %w", file, key, ErrProfileMismatch)
		}
	}

	return nil
}

// checkAsset verifies one image against its manifest entry and returns its
// SHA-256.
func checkAsset(fsys fs.FS, dir string, m manifest, name, file string, width, height int) (string, error) {
	ref, ok := m.Assets[name]
	if !ok || ref.Filename != file {
		return "", fmt.Errorf("manifest %s asset must name %s: %w", name, file, ErrInvalidManifest)
	}

	data, err := readFile(fsys, speciesPath(dir, file))
	if err != nil {
		return "", withTooLarge(err, ErrInvalidImage)
	}

	digest := sha256.Sum256(data)

	got := hex.EncodeToString(digest[:])
	if !sha256Pattern.MatchString(ref.SHA256) || ref.SHA256 != got {
		return "", fmt.Errorf("%s checksum %q does not match the file (%s): %w", name, ref.SHA256, got, ErrChecksumMismatch)
	}

	if err := validatePNG(data, width, height); err != nil {
		return "", fmt.Errorf("%s: %w", file, err)
	}

	return got, nil
}
