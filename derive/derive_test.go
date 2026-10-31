package derive_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/derive"
	"github.com/rusher2004/screeny-bird/fsutil"
)

const (
	width  = 320
	height = 180
)

var fixtureSpecies = []string{"1001-carolina-wren", "1002-house-finch", "1003-painted-bunting"}

type recordingProgress struct{ events []string }

func (r *recordingProgress) Planned(total, missing int) {
	r.events = append(r.events, fmt.Sprintf("planned %d %d", total, missing))
}

func (r *recordingProgress) Derived(done, missing int, species string) {
	r.events = append(r.events, fmt.Sprintf("derived %d/%d %s", done, missing, species))
}

// newStore builds a real catalog directory from the fixture species.
func newStore(t *testing.T, names ...string) catalog.DirLoader {
	t.Helper()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	addSpecies(t, dir, names...)
	rebuild(t, loader)

	return loader
}

func addSpecies(t *testing.T, dir string, names ...string) {
	t.Helper()

	for _, name := range names {
		from := filepath.Join("..", "testdata", "species", name)
		to := filepath.Join(dir, "species", name)

		if err := os.CopyFS(to, os.DirFS(from)); err != nil {
			t.Fatalf("copying %s: %v", name, err)
		}
	}
}

func rebuild(t *testing.T, loader catalog.DirLoader) {
	t.Helper()

	if _, err := loader.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
}

func newDeriver(t *testing.T, store catalog.DirLoader, progress *recordingProgress, out string) derive.Deriver {
	t.Helper()

	deriver, err := derive.NewDeriver(store, progress, out, width, height)
	if err != nil {
		t.Fatalf("NewDeriver() error = %v", err)
	}

	return deriver
}

type indexFile struct {
	Height        int `json:"height"`
	SchemaVersion int `json:"schema_version"`
	Species       []struct {
		Path    string `json:"path"`
		SHA256  string `json:"sha256"`
		Slug    string `json:"slug"`
		TaxonID int    `json:"taxon_id"`
	} `json:"species"`
	Width int `json:"width"`
}

func readIndex(t *testing.T, out string) (indexFile, []byte) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(out, "index.json"))
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}

	var idx indexFile
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("decoding index: %v", err)
	}

	return idx, raw
}

func decode(t *testing.T, path string) image.Image {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	return img
}

func TestNewDeriverReportsEveryProblemTogether(t *testing.T) {
	t.Parallel()

	_, err := derive.NewDeriver(nil, nil, " ", 0, -1)
	if !errors.Is(err, derive.ErrInvalidDeriver) {
		t.Fatalf("NewDeriver() error = %v, want ErrInvalidDeriver", err)
	}

	for _, part := range []string{"store", "progress", "output directory", "size"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not mention %s", err, part)
		}
	}

	for _, size := range [][2]int{{16385, 100}, {100, 16385}} {
		if _, err := derive.NewDeriver(newStore(t), &recordingProgress{}, "/out", size[0], size[1]); !errors.Is(err, derive.ErrInvalidDeriver) {
			t.Errorf("NewDeriver() at %dx%d error = %v, want ErrInvalidDeriver", size[0], size[1], err)
		}
	}

	if _, err := derive.NewDeriver(newStore(t), &recordingProgress{}, "/out", 1, 1); err != nil {
		t.Fatalf("NewDeriver() with every argument error = %v", err)
	}
}

func TestDeriveWritesEverySpeciesAtTheConfiguredSize(t *testing.T) {
	t.Parallel()

	store, out, progress := newStore(t, fixtureSpecies...), t.TempDir(), &recordingProgress{}

	added, err := newDeriver(t, store, progress, out).Derive(context.Background())
	if err != nil || added != 3 {
		t.Fatalf("Derive() = %d, %v; want 3 added", added, err)
	}

	idx, raw := readIndex(t, out)
	if idx.SchemaVersion != 1 || idx.Width != width || idx.Height != height || len(idx.Species) != 3 {
		t.Fatalf("index = %+v, want schema 1, %dx%d and three species", idx, width, height)
	}

	if !bytes.HasSuffix(raw, []byte("}\n")) || !bytes.Contains(raw, []byte("\n  \"height\"")) {
		t.Errorf("index is not canonical: %q", raw)
	}

	for i, e := range idx.Species {
		wantDir := fixtureSpecies[i]
		if want := fmt.Sprintf("species/%s/%dx%d.png", wantDir, width, height); e.Path != want {
			t.Errorf("species %d path = %q, want %q", i, e.Path, want)
		}

		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(e.Path)))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Path, err)
		}

		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != e.SHA256 {
			t.Errorf("%s: recorded checksum does not match the file", e.Path)
		}

		if got := decode(t, filepath.Join(out, filepath.FromSlash(e.Path))).Bounds(); got != image.Rect(0, 0, width, height) {
			t.Errorf("%s bounds = %v, want %dx%d", e.Path, got, width, height)
		}
	}

	want := []string{
		"planned 3 3",
		"derived 1/3 " + fixtureSpecies[0],
		"derived 2/3 " + fixtureSpecies[1],
		"derived 3/3 " + fixtureSpecies[2],
	}
	if !slices.Equal(progress.events, want) {
		t.Fatalf("progress = %q, want %q", progress.events, want)
	}
}

// lightBorderPlate is a 1200x1600 plate with a light 40 pixel border around a
// dark interior, so a test can see where the plate lands and what color pads it.
func lightBorderPlate() image.Image {
	plate := image.NewGray(image.Rect(0, 0, 1200, 1600))

	for y := range 1600 {
		for x := range 1200 {
			if x < 40 || x >= 1160 || y < 40 || y >= 1560 {
				plate.SetGray(x, y, color.Gray{Y: 200})
			} else {
				plate.SetGray(x, y, color.Gray{Y: 50})
			}
		}
	}

	return plate
}

// newPlateStore builds a one-species catalog whose portrait image is plate.
func newPlateStore(t *testing.T, plate image.Image) catalog.DirLoader {
	t.Helper()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	addSpecies(t, dir, fixtureSpecies[0])

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, plate); err != nil {
		t.Fatalf("encoding plate: %v", err)
	}

	speciesDir := filepath.Join(dir, "species", fixtureSpecies[0])
	manifestPath := filepath.Join(speciesDir, "manifest.json")

	old, err := os.ReadFile(filepath.Join(speciesDir, "portrait.png"))
	if err != nil {
		t.Fatalf("reading portrait: %v", err)
	}

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}

	oldSum, newSum := sha256.Sum256(old), sha256.Sum256(encoded.Bytes())
	patched := bytes.ReplaceAll(manifest, []byte(hex.EncodeToString(oldSum[:])), []byte(hex.EncodeToString(newSum[:])))

	if bytes.Equal(patched, manifest) {
		t.Fatal("the manifest does not mention the portrait checksum")
	}

	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}

	if err := os.WriteFile(filepath.Join(speciesDir, "portrait.png"), encoded.Bytes(), 0o644); err != nil {
		t.Fatalf("writing portrait: %v", err)
	}

	store, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	rebuild(t, store)

	return store
}

func luma(img image.Image, x, y int) uint8 {
	return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
}

func TestDeriveLetterboxesTheWholePlateWithoutCropping(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	if _, err := newDeriver(t, newPlateStore(t, lightBorderPlate()), &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	img := decode(t, filepath.Join(out, "species", fixtureSpecies[0], fmt.Sprintf("%dx%d.png", width, height)))

	// A 1200x1600 plate fits 320x180 as 135x180 starting at column 92, so
	// both sides are padded with the plate's own border color.
	for _, x := range []int{0, 50, 91, 228, 300, width - 1} {
		for _, y := range []int{0, 90, height - 1} {
			if got := luma(img, x, y); got != 200 {
				t.Errorf("padding pixel (%d,%d) = %d, want the border color 200", x, y, got)
			}
		}
	}

	if got := luma(img, 160, 90); got != 50 {
		t.Errorf("plate center = %d, want the interior 50, so the plate was not drawn whole", got)
	}

	// The plate's own border band survives at the top edge of the plate area.
	if got := luma(img, 160, 2); got < 150 {
		t.Errorf("top edge of the plate = %d, want the light border: the plate was cropped", got)
	}
}

func TestDerivePadsWithTheOpaqueBorderPixelsOnly(t *testing.T) {
	t.Parallel()

	// The left half of the outer ring is transparent black, the rest opaque
	// 200: padding must be 200, not pulled toward black.
	plate := image.NewNRGBA(image.Rect(0, 0, 1200, 1600))

	for y := range 1600 {
		for x := range 1200 {
			switch {
			case x < 600 && (x < 40 || y < 40 || y >= 1560):
				plate.SetNRGBA(x, y, color.NRGBA{})
			case x < 40 || x >= 1160 || y < 40 || y >= 1560:
				plate.SetNRGBA(x, y, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
			default:
				plate.SetNRGBA(x, y, color.NRGBA{R: 50, G: 50, B: 50, A: 255})
			}
		}
	}

	out := t.TempDir()
	if _, err := newDeriver(t, newPlateStore(t, plate), &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	img := decode(t, filepath.Join(out, "species", fixtureSpecies[0], fmt.Sprintf("%dx%d.png", width, height)))
	if got := luma(img, 0, 90); got != 200 {
		t.Errorf("padding = %d, want 200 from the opaque border pixels", got)
	}
}

func TestDeriveRefusesAPlateWithAFullyTransparentBorder(t *testing.T) {
	t.Parallel()

	plate := image.NewNRGBA(image.Rect(0, 0, 1200, 1600))

	for y := range 1600 {
		for x := range 1200 {
			if x >= 40 && x < 1160 && y >= 40 && y < 1560 {
				plate.SetNRGBA(x, y, color.NRGBA{R: 50, G: 50, B: 50, A: 255})
			}
		}
	}

	out := t.TempDir()

	_, err := newDeriver(t, newPlateStore(t, plate), &recordingProgress{}, out).Derive(context.Background())
	if !errors.Is(err, derive.ErrTransparentBorder) {
		t.Fatalf("Derive() error = %v, want ErrTransparentBorder", err)
	}
}

func TestDeriveIsDeterministic(t *testing.T) {
	t.Parallel()

	store := newStore(t, fixtureSpecies...)

	first, second := t.TempDir(), t.TempDir()

	for _, out := range []string{first, second} {
		if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
			t.Fatalf("Derive() error = %v", err)
		}
	}

	for _, name := range []string{"index.json", filepath.Join("species", fixtureSpecies[1], fmt.Sprintf("%dx%d.png", width, height))} {
		a, errA := os.ReadFile(filepath.Join(first, name))
		b, errB := os.ReadFile(filepath.Join(second, name))

		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Errorf("%s differs between two runs (errors %v, %v)", name, errA, errB)
		}
	}
}

func TestDeriveAddsOnlyTheNewSpecies(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	store, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	addSpecies(t, dir, fixtureSpecies[:2]...)
	rebuild(t, store)

	if added, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil || added != 2 {
		t.Fatalf("first Derive() = %d, %v; want 2", added, err)
	}

	firstPath := filepath.Join(out, "species", fixtureSpecies[0], fmt.Sprintf("%dx%d.png", width, height))

	before, err := os.Stat(firstPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	addSpecies(t, dir, fixtureSpecies[2])
	rebuild(t, store)

	progress := &recordingProgress{}
	if added, err := newDeriver(t, store, progress, out).Derive(context.Background()); err != nil || added != 1 {
		t.Fatalf("second Derive() = %d, %v; want 1", added, err)
	}

	if want := []string{"planned 3 1", "derived 1/1 " + fixtureSpecies[2]}; !slices.Equal(progress.events, want) {
		t.Fatalf("progress = %q, want %q", progress.events, want)
	}

	after, err := os.Stat(firstPath)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the existing derived image was rewritten (stat error %v)", err)
	}

	if idx, _ := readIndex(t, out); len(idx.Species) != 3 {
		t.Errorf("index lists %d species, want 3", len(idx.Species))
	}

	if added, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil || added != 0 {
		t.Fatalf("third Derive() = %d, %v; want nothing to add", added, err)
	}
}

// cancelAfter cancels a context once n images are derived.
type cancelAfter struct {
	recordingProgress
	n      int
	cancel context.CancelFunc
}

func (c *cancelAfter) Derived(done, missing int, species string) {
	c.recordingProgress.Derived(done, missing, species)

	if done == c.n {
		c.cancel()
	}
}

func TestDeriveRecordsEachImageSoAStoppedRunResumes(t *testing.T) {
	t.Parallel()

	store, out := newStore(t, fixtureSpecies...), t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	stopper := &cancelAfter{n: 1, cancel: cancel}

	deriver, err := derive.NewDeriver(store, stopper, out, width, height)
	if err != nil {
		t.Fatalf("NewDeriver() error = %v", err)
	}

	if _, err := deriver.Derive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Derive() error = %v, want context.Canceled", err)
	}

	if idx, _ := readIndex(t, out); len(idx.Species) != 1 || idx.Species[0].Slug != "carolina-wren" {
		t.Fatalf("index after the stop lists %+v, want only the finished image", idx.Species)
	}

	progress := &recordingProgress{}
	if added, err := newDeriver(t, store, progress, out).Derive(context.Background()); err != nil || added != 2 {
		t.Fatalf("second Derive() = %d, %v; want the remaining 2", added, err)
	}

	if want := []string{"planned 3 2", "derived 1/2 " + fixtureSpecies[1], "derived 2/2 " + fixtureSpecies[2]}; !slices.Equal(progress.events, want) {
		t.Fatalf("progress = %q, want %q", progress.events, want)
	}
}

func TestDeriveAdoptsAnIdenticalUnrecordedImageButNeverOverwritesADifferentOne(t *testing.T) {
	t.Parallel()

	store, out := newStore(t, fixtureSpecies[:2]...), t.TempDir()

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	size := fmt.Sprintf("%dx%d.png", width, height)
	first := filepath.Join(out, "species", fixtureSpecies[0], size)
	second := filepath.Join(out, "species", fixtureSpecies[1], size)

	before, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Losing the index leaves both images unrecorded.
	if err := os.Remove(filepath.Join(out, "index.json")); err != nil {
		t.Fatalf("removing index: %v", err)
	}

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() with identical leftovers error = %v", err)
	}

	after, err := os.Stat(first)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an identical leftover was rewritten instead of adopted (stat error %v)", err)
	}

	if idx, _ := readIndex(t, out); len(idx.Species) != 2 {
		t.Errorf("index lists %d species after adopting, want 2", len(idx.Species))
	}

	if err := os.Remove(filepath.Join(out, "index.json")); err != nil {
		t.Fatalf("removing index: %v", err)
	}

	if err := os.WriteFile(second, []byte("hand edited"), 0o644); err != nil {
		t.Fatalf("editing: %v", err)
	}

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); !errors.Is(err, derive.ErrDerivedChanged) {
		t.Fatalf("Derive() over a changed leftover error = %v, want ErrDerivedChanged", err)
	}

	if got, err := os.ReadFile(second); err != nil || string(got) != "hand edited" {
		t.Errorf("the changed leftover was overwritten: %q, %v", got, err)
	}
}

func TestDeriveFailsOnARecordedSpeciesTheCatalogLacks(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	if _, err := newDeriver(t, newStore(t, fixtureSpecies...), &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	_, err := newDeriver(t, newStore(t, fixtureSpecies[:2]...), &recordingProgress{}, out).Derive(context.Background())
	if !errors.Is(err, derive.ErrInvalidIndex) || !strings.Contains(err.Error(), "1003") {
		t.Fatalf("Derive() error = %v, want ErrInvalidIndex naming taxon 1003", err)
	}
}

func TestDeriveRefusesAChangedSize(t *testing.T) {
	t.Parallel()

	store, out := newStore(t, fixtureSpecies[0]), t.TempDir()

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	other, err := derive.NewDeriver(store, &recordingProgress{}, out, width*2, height*2)
	if err != nil {
		t.Fatalf("NewDeriver() error = %v", err)
	}

	if _, err := other.Derive(context.Background()); !errors.Is(err, derive.ErrSizeChanged) {
		t.Fatalf("Derive() at another size error = %v, want ErrSizeChanged", err)
	}
}

func TestDeriveReportsEveryChangedOrMissingImage(t *testing.T) {
	t.Parallel()

	store, out := newStore(t, fixtureSpecies...), t.TempDir()

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	size := fmt.Sprintf("%dx%d.png", width, height)
	if err := os.WriteFile(filepath.Join(out, "species", fixtureSpecies[0], size), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tampering: %v", err)
	}

	if err := os.Remove(filepath.Join(out, "species", fixtureSpecies[1], size)); err != nil {
		t.Fatalf("removing: %v", err)
	}

	_, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background())
	if !errors.Is(err, derive.ErrDerivedChanged) {
		t.Fatalf("Derive() error = %v, want ErrDerivedChanged", err)
	}

	for _, name := range fixtureSpecies[:2] {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

func TestDeriveRejectsAnIndexThatPointsElsewhere(t *testing.T) {
	t.Parallel()

	store, out := newStore(t, fixtureSpecies[0]), t.TempDir()

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	idx, raw := readIndex(t, out)
	tampered := bytes.Replace(raw, []byte(idx.Species[0].Path), []byte("../../etc/passwd"), 1)

	if err := os.WriteFile(filepath.Join(out, "index.json"), tampered, 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); !errors.Is(err, derive.ErrInvalidIndex) {
		t.Fatalf("Derive() error = %v, want ErrInvalidIndex", err)
	}
}

func TestDeriveRejectsAMalformedIndex(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"not json":       "{",
		"unknown field":  `{"schema_version":1,"width":320,"height":180,"species":[],"extra":1}`,
		"wrong schema":   `{"schema_version":2,"width":320,"height":180,"species":[]}`,
		"duplicate taxa": `{"schema_version":1,"width":320,"height":180,"species":[{"path":"a","sha256":"b","slug":"c","taxon_id":1},{"path":"a","sha256":"b","slug":"c","taxon_id":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := t.TempDir()
			if err := os.WriteFile(filepath.Join(out, "index.json"), []byte(content), 0o644); err != nil {
				t.Fatalf("writing index: %v", err)
			}

			if _, err := newDeriver(t, newStore(t, fixtureSpecies[0]), &recordingProgress{}, out).Derive(context.Background()); !errors.Is(err, derive.ErrInvalidIndex) {
				t.Fatalf("Derive() error = %v, want ErrInvalidIndex", err)
			}
		})
	}
}

func TestDeriveRefusesAnInvalidCatalogAndWritesNothing(t *testing.T) {
	t.Parallel()

	loader, err := catalog.NewDirLoader(t.TempDir())
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	out := t.TempDir()

	if _, err := newDeriver(t, loader, &recordingProgress{}, out).Derive(context.Background()); !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Derive() error = %v, want catalog.ErrMissingFile", err)
	}

	if _, err := os.Stat(filepath.Join(out, "index.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an index was written for an invalid catalog (stat error %v)", err)
	}
}

func TestDeriveStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := newDeriver(t, newStore(t, fixtureSpecies[0]), &recordingProgress{}, t.TempDir()).Derive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Derive() error = %v, want context.Canceled", err)
	}
}

func TestDeriveRefusesToRunWhileAnotherRunHoldsTheLock(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	lock, err := fsutil.AcquireLock(filepath.Join(out, "derive.lock"))
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}

	defer func() {
		if err := lock.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}()

	if _, err := newDeriver(t, newStore(t, fixtureSpecies[0]), &recordingProgress{}, out).Derive(context.Background()); !errors.Is(err, fsutil.ErrLocked) {
		t.Fatalf("Derive() error = %v, want fsutil.ErrLocked", err)
	}
}
