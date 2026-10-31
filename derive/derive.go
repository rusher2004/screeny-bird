// Package derive builds the screensaver-sized images from the approved
// catalog. Each upright portrait plate (the display variant is rotated for a
// landscape e-ink frame, so its text runs sideways) is scaled whole to fit the
// configured size and padded
// with a solid color taken from its own border, so nothing is cropped. The
// output is deterministic and lives outside the catalog, which is never
// changed.
package derive

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/fsutil"
)

const (
	indexFile     = "index.json"
	lockFile      = "derive.lock"
	schemaVersion = 1
)

var (
	// ErrInvalidDeriver reports a missing or unusable constructor argument.
	ErrInvalidDeriver = errors.New("invalid deriver")
	// ErrSizeChanged reports that the derived images were made at a different
	// size than the one configured now. Derived images are never replaced, so
	// the operator removes the directory to start over.
	ErrSizeChanged = errors.New("derived size changed")
	// ErrDerivedChanged reports a recorded derived image that is missing or no
	// longer matches the checksum recorded for it.
	ErrDerivedChanged = errors.New("derived image changed")
	// ErrInvalidIndex reports a derived index that is malformed or points
	// somewhere other than where its species belongs.
	ErrInvalidIndex = errors.New("invalid derived index")
)

// store is what a deriver needs from the approved catalog: the validated
// species and the bytes of their images.
type store interface {
	Load() (*catalog.Catalog, error)
	ReadAsset(name string) ([]byte, error)
}

// progress is told how a run is going, so whoever runs it can show progress.
// The deriver itself never logs.
type progress interface {
	// Planned reports how many species the catalog has and how many lack a
	// derived image.
	Planned(total, missing int)
	// Derived reports that done of missing images are written.
	Derived(done, missing int, species string)
}

// Deriver builds the derived images for the species that lack one.
type Deriver struct {
	store    store
	progress progress
	outDir   string
	width    int
	height   int
}

// NewDeriver returns a deriver that writes width by height images into
// outDir. Every argument is required, and all problems are reported together.
func NewDeriver(store store, progress progress, outDir string, width, height int) (Deriver, error) {
	var problems []error

	if store == nil {
		problems = append(problems, errors.New("store is required"))
	}

	if progress == nil {
		problems = append(problems, errors.New("progress is required"))
	}

	if strings.TrimSpace(outDir) == "" {
		problems = append(problems, errors.New("output directory is required"))
	}

	if width < 1 || height < 1 {
		problems = append(problems, fmt.Errorf("size must be positive, got %dx%d", width, height))
	}

	if err := errors.Join(problems...); err != nil {
		return Deriver{}, fmt.Errorf("creating deriver: %w: %w", ErrInvalidDeriver, err)
	}

	return Deriver{store: store, progress: progress, outDir: outDir, width: width, height: height}, nil
}

type indexEntry struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Slug    string `json:"slug"`
	TaxonID int    `json:"taxon_id"`
}

type index struct {
	Height        int          `json:"height"`
	SchemaVersion int          `json:"schema_version"`
	Species       []indexEntry `json:"species"`
	Width         int          `json:"width"`
}

// Derive writes a derived image for every species in the catalog that lacks
// one and rewrites the derived index. It returns how many it wrote. A species
// that already has an image is only checked against its recorded checksum,
// never rewritten. A failed run leaves its finished images, and the next run
// makes the rest.
func (d Deriver) Derive(ctx context.Context) (added int, err error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("starting derive: %w", err)
	}

	if err := os.MkdirAll(d.outDir, 0o755); err != nil {
		return 0, fmt.Errorf("creating derived directory %s: %w", d.outDir, err)
	}

	lock, err := fsutil.AcquireLock(filepath.Join(d.outDir, lockFile))
	if err != nil {
		return 0, fmt.Errorf("starting derive: %w", err)
	}

	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("releasing the derive lock: %w", releaseErr))
		}
	}()

	loaded, err := d.store.Load()
	if err != nil {
		return 0, fmt.Errorf("loading the catalog: %w", err)
	}

	species := slices.Clone(loaded.Species())
	slices.SortFunc(species, func(a, b catalog.Species) int { return cmp.Compare(a.TaxonID, b.TaxonID) })

	recorded, err := d.readIndex()
	if err != nil {
		return 0, err
	}

	entries := make([]indexEntry, 0, len(species))

	var missing []catalog.Species

	var changed []error

	for _, sp := range species {
		old, ok := recorded[sp.TaxonID]
		if !ok {
			missing = append(missing, sp)

			continue
		}

		if err := d.checkRecorded(sp, old); err != nil {
			changed = append(changed, err)

			continue
		}

		entries = append(entries, old)
	}

	if err := errors.Join(changed...); err != nil {
		return 0, err
	}

	d.progress.Planned(len(species), len(missing))

	for i, sp := range missing {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("deriving %s: %w", speciesDir(sp), err)
		}

		entry, err := d.deriveOne(sp)
		if err != nil {
			return 0, err
		}

		entries = append(entries, entry)

		d.progress.Derived(i+1, len(missing), speciesDir(sp))
	}

	slices.SortFunc(entries, func(a, b indexEntry) int { return cmp.Compare(a.TaxonID, b.TaxonID) })

	if err := d.writeIndex(entries); err != nil {
		return 0, err
	}

	return len(missing), nil
}

func speciesDir(sp catalog.Species) string {
	return fmt.Sprintf("%d-%s", sp.TaxonID, sp.Slug)
}

// derivedPath is where a species' image lives, relative to the derived
// directory and slash-separated.
func (d Deriver) derivedPath(sp catalog.Species) string {
	return fmt.Sprintf("species/%s/%dx%d.png", speciesDir(sp), d.width, d.height)
}

// readIndex returns the recorded entries by taxon. A directory with no index
// has recorded nothing.
func (d Deriver) readIndex() (map[int]indexEntry, error) {
	raw, err := os.ReadFile(filepath.Join(d.outDir, indexFile))
	if errors.Is(err, fs.ErrNotExist) {
		return map[int]indexEntry{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the derived index: %w", err)
	}

	var idx index

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&idx); err != nil {
		return nil, fmt.Errorf("decoding the derived index: %w: %w", ErrInvalidIndex, err)
	}

	if idx.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("derived index schema version %d, want %d: %w", idx.SchemaVersion, schemaVersion, ErrInvalidIndex)
	}

	if idx.Width != d.width || idx.Height != d.height {
		return nil, fmt.Errorf("derived images are %dx%d but %dx%d is configured: %w", idx.Width, idx.Height, d.width, d.height, ErrSizeChanged)
	}

	byTaxon := make(map[int]indexEntry, len(idx.Species))

	for _, e := range idx.Species {
		if _, dup := byTaxon[e.TaxonID]; dup {
			return nil, fmt.Errorf("taxon %d appears twice: %w", e.TaxonID, ErrInvalidIndex)
		}

		byTaxon[e.TaxonID] = e
	}

	return byTaxon, nil
}

// checkRecorded confirms a recorded image is where its species belongs and
// still has the recorded checksum.
func (d Deriver) checkRecorded(sp catalog.Species, e indexEntry) error {
	if want := d.derivedPath(sp); e.Path != want || e.Slug != sp.Slug {
		return fmt.Errorf("%s is recorded at %q, want %q: %w", speciesDir(sp), e.Path, want, ErrInvalidIndex)
	}

	data, err := os.ReadFile(filepath.Join(d.outDir, filepath.FromSlash(e.Path)))
	if err != nil {
		return fmt.Errorf("reading the derived image of %s: %w: %w", speciesDir(sp), ErrDerivedChanged, err)
	}

	if got := sha256Hex(data); got != e.SHA256 {
		return fmt.Errorf("the derived image of %s has checksum %s, recorded %s: %w", speciesDir(sp), got, e.SHA256, ErrDerivedChanged)
	}

	return nil
}

func (d Deriver) deriveOne(sp catalog.Species) (indexEntry, error) {
	dir := speciesDir(sp)

	data, err := d.store.ReadAsset(sp.PortraitPath)
	if err != nil {
		return indexEntry{}, fmt.Errorf("reading the portrait image of %s: %w", dir, err)
	}

	if got := sha256Hex(data); got != sp.PortraitSHA256 {
		return indexEntry{}, fmt.Errorf("the portrait image of %s has checksum %s, catalog says %s: %w", dir, got, sp.PortraitSHA256, catalog.ErrChecksumMismatch)
	}

	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return indexEntry{}, fmt.Errorf("decoding the portrait image of %s: %w", dir, err)
	}

	var out bytes.Buffer
	if err := png.Encode(&out, fit(src, d.width, d.height)); err != nil {
		return indexEntry{}, fmt.Errorf("encoding the derived image of %s: %w", dir, err)
	}

	rel := d.derivedPath(sp)
	target := filepath.Join(d.outDir, filepath.FromSlash(rel))

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return indexEntry{}, fmt.Errorf("creating the derived directory of %s: %w", dir, err)
	}

	if err := fsutil.WriteFileAtomic(target, out.Bytes(), 0o644); err != nil {
		return indexEntry{}, fmt.Errorf("writing the derived image of %s: %w", dir, err)
	}

	return indexEntry{Path: rel, SHA256: sha256Hex(out.Bytes()), Slug: sp.Slug, TaxonID: sp.TaxonID}, nil
}

// writeIndex writes the derived index in canonical form: sorted keys, two
// space indent and a trailing newline.
func (d Deriver) writeIndex(entries []indexEntry) error {
	raw, err := json.MarshalIndent(index{Height: d.height, SchemaVersion: schemaVersion, Species: entries, Width: d.width}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the derived index: %w", err)
	}

	if err := fsutil.WriteFileAtomic(filepath.Join(d.outDir, indexFile), append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing the derived index: %w", err)
	}

	return nil
}

// fit scales src whole to the largest size that fits width by height and
// centers it on a canvas filled with the color of src's border.
func fit(src image.Image, width, height int) *image.NRGBA {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()

	var w, h int
	if sw*height >= sh*width {
		w, h = width, max(1, (sh*width+sw/2)/sw)
	} else {
		w, h = max(1, (sw*height+sh/2)/sh), height
	}

	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(borderColor(src)), image.Point{}, draw.Src)

	left, top := (width-w)/2, (height-h)/2
	xdraw.CatmullRom.Scale(dst, image.Rect(left, top, left+w, top+h), src, bounds, xdraw.Over, nil)

	return dst
}

// borderColor is the mean color of the outermost ring of pixels of img.
func borderColor(img image.Image) color.NRGBA {
	b := img.Bounds()

	var r, g, bl, n int

	add := func(x, y int) {
		c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
		r, g, bl, n = r+int(c.R), g+int(c.G), bl+int(c.B), n+1
	}

	for x := b.Min.X; x < b.Max.X; x++ {
		add(x, b.Min.Y)
		add(x, b.Max.Y-1)
	}

	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		add(b.Min.X, y)
		add(b.Max.X-1, y)
	}

	return color.NRGBA{R: uint8((r + n/2) / n), G: uint8((g + n/2) / n), B: uint8((bl + n/2) / n), A: 255}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}
