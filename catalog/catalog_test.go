package catalog_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

var (
	portraitPNG = mustPNG(1200, 1600)
	displayPNG  = mustPNG(1600, 1200)
)

func mustPNG(width, height int) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		panic(fmt.Sprintf("encoding fixture PNG: %v", err))
	}

	return buf.Bytes()
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:])
}

// speciesFixture identifies one species in a test catalog.
type speciesFixture struct {
	TaxonID    int
	Common     string
	Scientific string
	Slug       string
	ApprovedAt string
}

func (s speciesFixture) dir() string {
	return fmt.Sprintf("%d-%s", s.TaxonID, s.Slug)
}

var (
	wren = speciesFixture{
		TaxonID: 1001, Common: "Carolina Wren", Scientific: "Thryothorus ludovicianus",
		Slug: "carolina-wren", ApprovedAt: "2026-07-01T10:00:00+00:00",
	}
	finch = speciesFixture{
		TaxonID: 1002, Common: "house Finch", Scientific: "Haemorhous mexicanus",
		Slug: "house-finch", ApprovedAt: "2026-08-09T17:12:25+00:00",
	}
	bunting = speciesFixture{
		TaxonID: 1003, Common: "Painted Bunting", Scientific: "Passerina ciris",
		Slug: "painted-bunting", ApprovedAt: "2026-06-15T08:30:00+00:00",
	}
)

// speciesFiles is the editable content of one species directory. Edit hooks
// receive these before the fixture is written, so a test changes exactly the
// thing it is about.
type speciesFiles struct {
	manifest map[string]any
	portrait []byte
	display  []byte
	// profile and review default to copies of the manifest's values. Setting
	// omitProfile or omitReview leaves the file out.
	profile     any
	review      any
	omitProfile bool
	omitReview  bool
	extra       map[string][]byte
}

func (f *speciesFiles) generation() map[string]any {
	return f.manifest["generation"].(map[string]any)
}

func (f *speciesFiles) qualityReview() map[string]any {
	return f.manifest["quality_review"].(map[string]any)
}

func validReview() map[string]any {
	return map[string]any{
		"passed":              true,
		"species_accuracy":    5,
		"anatomy_accuracy":    5,
		"text_accuracy":       5,
		"composition_quality": 5,
		"location_free":       true,
		"findings":            []any{"Matches the references."},
		"correction_findings": []any{},
		"verification_sources": []any{
			map[string]any{"title": "Source one", "url": "https://example.test/one"},
			map[string]any{"title": "Source two", "url": "https://example.test/two"},
		},
	}
}

func newSpeciesFiles(s speciesFixture) *speciesFiles {
	return &speciesFiles{
		manifest: map[string]any{
			"schema_version":  1,
			"status":          "approved",
			"taxon_id":        s.TaxonID,
			"common_name":     s.Common,
			"scientific_name": s.Scientific,
			"slug":            s.Slug,
			"approved_at":     s.ApprovedAt,
			"profile":         map[string]any{"taxon_id": s.TaxonID, "habitat": "Dense brush"},
			"references":      []any{},
			"generation": map[string]any{
				"generator":      "test generator",
				"prompt_version": "field-journal-v6",
				"generated_at":   s.ApprovedAt,
				"attempt":        1,
				"max_attempts":   3,
			},
			"quality_review": validReview(),
			"assets": map[string]any{
				"portrait": map[string]any{"filename": "portrait.png", "sha256": ""},
				"display":  map[string]any{"filename": "display.png", "sha256": ""},
			},
		},
		portrait: portraitPNG,
		display:  displayPNG,
		extra:    map[string][]byte{},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encoding fixture JSON: %v", err)
	}

	return append(data, '\n')
}

// build writes a valid catalog containing species. The edit hook, if any,
// changes each species' files by directory name before they are written. Asset
// checksums that a test left empty are computed from the final file bytes.
func build(t *testing.T, species []speciesFixture, edit func(dir string, f *speciesFiles)) fstest.MapFS {
	t.Helper()

	// The index lists species by case-folded common name, then taxon ID.
	species = slices.Clone(species)
	slices.SortFunc(species, func(a, b speciesFixture) int {
		if c := strings.Compare(strings.ToLower(a.Common), strings.ToLower(b.Common)); c != 0 {
			return c
		}

		return a.TaxonID - b.TaxonID
	})

	fsys := fstest.MapFS{}
	index := map[string]any{"schema_version": 1, "generated_at": nil}
	entries := []any{}
	latest := ""

	for _, s := range species {
		files := newSpeciesFiles(s)
		if edit != nil {
			edit(s.dir(), files)
		}

		assets := files.manifest["assets"].(map[string]any)
		checksums := map[string]string{}

		for name, data := range map[string][]byte{"portrait": files.portrait, "display": files.display} {
			asset, ok := assets[name].(map[string]any)
			if !ok {
				// A test removed this asset from the manifest on purpose.
				checksums[name] = sum(data)

				continue
			}

			if asset["sha256"] == "" {
				asset["sha256"] = sum(data)
			}

			checksums[name] = asset["sha256"].(string)
		}

		dir := s.dir()
		fsys["species/"+dir+"/manifest.json"] = &fstest.MapFile{Data: mustJSON(t, files.manifest)}
		fsys["species/"+dir+"/portrait.png"] = &fstest.MapFile{Data: files.portrait}
		fsys["species/"+dir+"/display.png"] = &fstest.MapFile{Data: files.display}

		if !files.omitProfile {
			profile := files.profile
			if profile == nil {
				profile = files.manifest["profile"]
			}

			fsys["species/"+dir+"/profile.json"] = &fstest.MapFile{Data: mustJSON(t, profile)}
		}

		if !files.omitReview {
			review := files.review
			if review == nil {
				review = files.manifest["quality_review"]
			}

			fsys["species/"+dir+"/quality-review.json"] = &fstest.MapFile{Data: mustJSON(t, review)}
		}

		for name, data := range files.extra {
			fsys["species/"+dir+"/"+name] = &fstest.MapFile{Data: data}
		}

		entries = append(entries, map[string]any{
			"taxon_id":        s.TaxonID,
			"common_name":     s.Common,
			"scientific_name": s.Scientific,
			"slug":            s.Slug,
			"portrait_path":   "species/" + dir + "/portrait.png",
			"portrait_sha256": checksums["portrait"],
			"display_path":    "species/" + dir + "/display.png",
			"display_sha256":  checksums["display"],
			"approved_at":     s.ApprovedAt,
		})

		if s.ApprovedAt > latest {
			latest = s.ApprovedAt
		}
	}

	if latest != "" {
		index["generated_at"] = latest
	}

	index["species"] = entries
	fsys["index.json"] = &fstest.MapFile{Data: mustJSON(t, index)}

	return fsys
}

func mustLoad(t *testing.T, fsys fs.FS) *catalog.Catalog {
	t.Helper()

	got, err := catalog.Load(fsys)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	return got
}

func TestLoadValidCatalog(t *testing.T) {
	t.Parallel()

	got := mustLoad(t, build(t, []speciesFixture{wren, finch, bunting}, nil))

	species := got.Species()
	if len(species) != 3 {
		t.Fatalf("Species() has %d entries, want 3", len(species))
	}

	// Sorted by case-folded common name: carolina, house, painted.
	var gotOrder []int
	for _, s := range species {
		gotOrder = append(gotOrder, s.TaxonID)
	}

	want := []int{wren.TaxonID, finch.TaxonID, bunting.TaxonID}
	if fmt.Sprint(gotOrder) != fmt.Sprint(want) {
		t.Fatalf("Species() order = %v, want %v", gotOrder, want)
	}

	first := species[0]
	if first.CommonName != wren.Common || first.Slug != wren.Slug ||
		first.PortraitPath != "species/1001-carolina-wren/portrait.png" ||
		first.DisplayPath != "species/1001-carolina-wren/display.png" ||
		first.PortraitSHA256 != sum(portraitPNG) || first.DisplaySHA256 != sum(displayPNG) ||
		first.ApprovedAt != wren.ApprovedAt || first.ScientificName != wren.Scientific {
		t.Fatalf("Species()[0] = %+v, does not match the fixture", first)
	}
}

func TestLoadEmptyCatalog(t *testing.T) {
	t.Parallel()

	fsys := build(t, nil, nil)
	fsys["species"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}

	got := mustLoad(t, fsys)
	if len(got.Species()) != 0 {
		t.Fatalf("Species() = %v, want none", got.Species())
	}
}

func TestLoadRejectsBadStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(fsys fstest.MapFS)
		want error
	}{
		{
			name: "unexpected root entry",
			edit: func(fsys fstest.MapFS) { fsys["notes.txt"] = &fstest.MapFile{Data: []byte("x")} },
			want: catalog.ErrUnexpectedFile,
		},
		{
			name: "missing index",
			edit: func(fsys fstest.MapFS) { delete(fsys, "index.json") },
			want: catalog.ErrMissingFile,
		},
		{
			name: "missing species root",
			edit: func(fsys fstest.MapFS) {
				for name := range fsys {
					if strings.HasPrefix(name, "species/") {
						delete(fsys, name)
					}
				}
			},
			want: catalog.ErrMissingFile,
		},
		{
			name: "unexpected species file",
			edit: func(fsys fstest.MapFS) {
				fsys["species/1001-carolina-wren/README.md"] = &fstest.MapFile{Data: []byte("x")}
			},
			want: catalog.ErrUnexpectedFile,
		},
		{
			name: "missing manifest",
			edit: func(fsys fstest.MapFS) { delete(fsys, "species/1001-carolina-wren/manifest.json") },
			want: catalog.ErrMissingFile,
		},
		{
			name: "missing portrait",
			edit: func(fsys fstest.MapFS) { delete(fsys, "species/1001-carolina-wren/portrait.png") },
			want: catalog.ErrMissingFile,
		},
		{
			name: "symlinked species directory",
			edit: func(fsys fstest.MapFS) {
				for name := range fsys {
					if strings.HasPrefix(name, "species/1001-carolina-wren/") {
						delete(fsys, name)
					}
				}

				fsys["species/1001-carolina-wren"] = &fstest.MapFile{Data: []byte("elsewhere"), Mode: fs.ModeSymlink}
			},
			want: catalog.ErrNotRegularFile,
		},
		{
			name: "symlinked manifest",
			edit: func(fsys fstest.MapFS) {
				fsys["species/1001-carolina-wren/manifest.json"] = &fstest.MapFile{
					Data: []byte("elsewhere"), Mode: fs.ModeSymlink,
				}
			},
			want: catalog.ErrNotRegularFile,
		},
		{
			name: "directory name does not match manifest identity",
			edit: func(fsys fstest.MapFS) {
				for name, file := range fsys {
					if rest, ok := strings.CutPrefix(name, "species/1001-carolina-wren/"); ok {
						fsys["species/1001-renamed/"+rest] = file
						delete(fsys, name)
					}
				}
			},
			want: catalog.ErrIdentityMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := build(t, []speciesFixture{wren}, nil)
			tt.edit(fsys)

			got, err := catalog.Load(fsys)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Load() error = %v, want %v", err, tt.want)
			}

			if got != nil {
				t.Fatalf("Load() = %v with an error, want no partial catalog", got)
			}
		})
	}
}

func TestLoadRejectsDuplicateTaxon(t *testing.T) {
	t.Parallel()

	twin := wren
	twin.Slug = "carolina-wren-two"
	twin.Common = "Carolina Wren Two"

	_, err := catalog.Load(build(t, []speciesFixture{wren, twin}, nil))
	if !errors.Is(err, catalog.ErrDuplicateTaxon) {
		t.Fatalf("Load() error = %v, want ErrDuplicateTaxon", err)
	}
}

func TestLoadReportsEveryBadSpecies(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren, finch, bunting}, func(dir string, f *speciesFiles) {
		if dir != finch.dir() {
			f.qualityReview()["passed"] = false
		}
	})

	_, err := catalog.Load(fsys)
	if !errors.Is(err, catalog.ErrReviewNotPublishable) {
		t.Fatalf("Load() error = %v, want ErrReviewNotPublishable", err)
	}

	for _, dir := range []string{wren.dir(), bunting.dir()} {
		if !strings.Contains(err.Error(), dir) {
			t.Errorf("Load() error does not name %s: %v", dir, err)
		}
	}

	if strings.Contains(err.Error(), finch.dir()) {
		t.Errorf("Load() error names the valid species %s: %v", finch.dir(), err)
	}
}

func TestSpeciesErrorIdentifiesDirectory(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) {
		f.qualityReview()["passed"] = false
	})

	_, err := catalog.Load(fsys)

	speciesErr, ok := errors.AsType[*catalog.SpeciesError](err)
	if !ok {
		t.Fatalf("Load() error = %v, want a *SpeciesError in the chain", err)
	}

	if speciesErr.Dir != wren.dir() {
		t.Fatalf("SpeciesError.Dir = %q, want %q", speciesErr.Dir, wren.dir())
	}

	if !errors.Is(speciesErr, catalog.ErrReviewNotPublishable) {
		t.Fatalf("SpeciesError does not unwrap to the cause: %v", speciesErr)
	}
}
