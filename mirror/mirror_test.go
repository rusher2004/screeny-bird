package mirror_test

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
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/fsutil"
	"github.com/rusher2004/screeny-bird/mirror"
	"github.com/rusher2004/screeny-bird/upstream"
)

const commit = "8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e"

func sum(data []byte) string {
	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:])
}

// pngBytes returns a small valid PNG of the given size; seed makes files differ.
func pngBytes(t *testing.T, width, height, seed int) []byte {
	t.Helper()

	img := image.NewGray(image.Rect(0, 0, width, height))
	img.SetGray(0, 0, color.Gray{Y: uint8(seed)})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding PNG: %v", err)
	}

	return buf.Bytes()
}

type fixture struct {
	TaxonID    int
	Common     string
	Scientific string
	Slug       string
	ApprovedAt string
	// Seed changes the image bytes, so two versions of a species differ.
	Seed int
	// Bare leaves out the optional profile.json and quality-review.json.
	Bare bool
}

func (f fixture) dir() string { return fmt.Sprintf("%d-%s", f.TaxonID, f.Slug) }

var (
	wren    = fixture{TaxonID: 1001, Common: "Carolina Wren", Scientific: "Thryothorus ludovicianus", Slug: "carolina-wren", ApprovedAt: "2026-07-01T10:00:00+00:00", Seed: 1}
	finch   = fixture{TaxonID: 1002, Common: "House Finch", Scientific: "Haemorhous mexicanus", Slug: "house-finch", ApprovedAt: "2026-08-09T17:12:25+00:00", Seed: 2}
	bunting = fixture{TaxonID: 1003, Common: "Painted Bunting", Scientific: "Passerina ciris", Slug: "painted-bunting", ApprovedAt: "2026-06-15T08:30:00+00:00", Seed: 3}
)

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encoding JSON: %v", err)
	}

	return append(data, '\n')
}

// speciesFiles builds the repository files of one valid species, keyed by
// their path under the catalog directory.
func speciesFiles(t *testing.T, f fixture) map[string][]byte {
	t.Helper()

	portrait := pngBytes(t, 1200, 1600, f.Seed)
	display := pngBytes(t, 1600, 1200, f.Seed)

	review := map[string]any{
		"passed": true, "species_accuracy": 5, "anatomy_accuracy": 5, "text_accuracy": 5,
		"composition_quality": 5, "location_free": true, "findings": []any{"Matches."},
		"correction_findings": []any{},
		"verification_sources": []any{
			map[string]any{"title": "One", "url": "https://example.test/one"},
			map[string]any{"title": "Two", "url": "https://example.test/two"},
		},
	}
	profile := map[string]any{"taxon_id": f.TaxonID, "habitat": "Dense brush"}

	manifest := map[string]any{
		"schema_version": 1, "status": "approved", "taxon_id": f.TaxonID, "common_name": f.Common,
		"scientific_name": f.Scientific, "slug": f.Slug, "approved_at": f.ApprovedAt,
		"profile": profile, "references": []any{},
		"generation": map[string]any{
			"generator": "test", "prompt_version": "field-journal-v6", "generated_at": f.ApprovedAt,
			"attempt": 1, "max_attempts": 3,
		},
		"quality_review": review,
		"assets": map[string]any{
			"portrait": map[string]any{"filename": "portrait.png", "sha256": sum(portrait)},
			"display":  map[string]any{"filename": "display.png", "sha256": sum(display)},
		},
	}

	base := path.Join("species", f.dir())
	files := map[string][]byte{
		path.Join(base, "manifest.json"): mustJSON(t, manifest),
		path.Join(base, "portrait.png"):  portrait,
		path.Join(base, "display.png"):   display,
	}

	if !f.Bare {
		files[path.Join(base, "profile.json")] = mustJSON(t, profile)
		files[path.Join(base, "quality-review.json")] = mustJSON(t, review)
	}

	return files
}

// fakeSource serves an in-memory upstream repository.
type fakeSource struct {
	commit     string
	resolveErr error
	files      map[string][]byte // keyed by repository path, e.g. catalog/index.json
	fetched    []string
}

func (f *fakeSource) ResolveRef(context.Context) (string, error) {
	return f.commit, f.resolveErr
}

func (f *fakeSource) Fetch(_ context.Context, gotCommit, filePath string) ([]byte, error) {
	f.fetched = append(f.fetched, filePath)

	if gotCommit != f.commit {
		return nil, fmt.Errorf("fetched at %q, want the resolved commit: %w", gotCommit, upstream.ErrBadResponse)
	}

	data, ok := f.files[filePath]
	if !ok {
		return nil, fmt.Errorf("%s: %w", filePath, upstream.ErrNotFound)
	}

	return data, nil
}

// fetchedSpecies returns how many species files (not the index) were fetched.
func (f *fakeSource) fetchedSpecies() int {
	n := 0

	for _, p := range f.fetched {
		if strings.HasPrefix(p, "catalog/species/") {
			n++
		}
	}

	return n
}

// newUpstream builds a repository holding the given species and a matching index.
func newUpstream(t *testing.T, species ...fixture) *fakeSource {
	t.Helper()

	source := &fakeSource{commit: commit, files: map[string][]byte{}}
	entries := make([]any, 0, len(species))

	latest := ""

	for _, f := range species {
		for p, data := range speciesFiles(t, f) {
			source.files[path.Join("catalog", p)] = data
		}

		entries = append(entries, map[string]any{
			"taxon_id": f.TaxonID, "common_name": f.Common, "scientific_name": f.Scientific, "slug": f.Slug,
			"portrait_path":   path.Join("species", f.dir(), "portrait.png"),
			"portrait_sha256": sum(pngBytes(t, 1200, 1600, f.Seed)),
			"display_path":    path.Join("species", f.dir(), "display.png"),
			"display_sha256":  sum(pngBytes(t, 1600, 1200, f.Seed)),
			"approved_at":     f.ApprovedAt,
		})

		if f.ApprovedAt > latest {
			latest = f.ApprovedAt
		}
	}

	index := map[string]any{"schema_version": 1, "generated_at": nil, "species": entries}
	if latest != "" {
		index["generated_at"] = latest
	}

	source.files["catalog/index.json"] = mustJSON(t, index)

	return source
}

type dirs struct{ store, work string }

func newDirs(t *testing.T) dirs {
	t.Helper()

	root := t.TempDir()
	d := dirs{store: filepath.Join(root, "store"), work: filepath.Join(root, "work")}

	if err := os.Mkdir(d.store, 0o755); err != nil {
		t.Fatalf("creating store: %v", err)
	}

	return d
}

// recordingProgress keeps what a syncer reports, one line per event.
type recordingProgress struct{ events []string }

func (r *recordingProgress) Resolved(commit string) {
	r.events = append(r.events, "resolved "+commit)
}

func (r *recordingProgress) Planned(upstream, missing int) {
	r.events = append(r.events, fmt.Sprintf("planned %d %d", upstream, missing))
}

func (r *recordingProgress) Staged(done, total int, species string, reused bool) {
	r.events = append(r.events, fmt.Sprintf("staged %d/%d %s reused=%t", done, total, species, reused))
}

func (r *recordingProgress) Validating() { r.events = append(r.events, "validating") }

func (r *recordingProgress) Publishing() { r.events = append(r.events, "publishing") }

func newSyncer(t *testing.T, source *fakeSource, d dirs) mirror.Syncer {
	t.Helper()

	return newSyncerReporting(t, source, &recordingProgress{}, d)
}

func newSyncerReporting(t *testing.T, source *fakeSource, progress *recordingProgress, d dirs) mirror.Syncer {
	t.Helper()

	syncer, err := mirror.NewSyncer(source, progress, "veteranbv/inky-bird-frame", "main", d.store, d.work)
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}

	return syncer
}

func mustLoadStore(t *testing.T, store string) *catalog.Catalog {
	t.Helper()

	loader, err := catalog.NewDirLoader(store)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	got, err := loader.Load()
	if err != nil {
		t.Fatalf("store does not load: %v", err)
	}

	return got
}

func taxa(c *catalog.Catalog) []int {
	var ids []int
	for _, s := range c.Species() {
		ids = append(ids, s.TaxonID)
	}

	slices.Sort(ids)

	return ids
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()

	got := map[string]string{}

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}

		got[rel] = string(data)

		return nil
	})
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}

	return got
}

func TestSyncFillsAnEmptyStore(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch)
	d := newDirs(t)

	gotCommit, added, err := newSyncer(t, source, d).Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if gotCommit != commit || added != 2 {
		t.Fatalf("Sync() = %q, %d; want %q, 2", gotCommit, added, commit)
	}

	if got := taxa(mustLoadStore(t, d.store)); !slices.Equal(got, []int{1001, 1002}) {
		t.Fatalf("store holds taxa %v, want [1001 1002]", got)
	}

	var provenance struct {
		Repo, Ref, Commit string
		Added             int
	}

	raw, err := os.ReadFile(filepath.Join(d.work, "upstream.json"))
	if err != nil {
		t.Fatalf("reading provenance: %v", err)
	}

	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatalf("decoding provenance: %v", err)
	}

	if provenance.Repo != "veteranbv/inky-bird-frame" || provenance.Ref != "main" || provenance.Commit != commit || provenance.Added != 2 {
		t.Fatalf("provenance = %+v, want repo, ref, commit and 2 added", provenance)
	}

	left := readTree(t, d.work)
	for name := range left {
		if name != "upstream.json" && name != "sync.lock" {
			t.Errorf("work directory kept %q; staging must be cleaned up", name)
		}
	}
}

func TestSyncCreatesAMissingStoreSpeciesRoot(t *testing.T) {
	t.Parallel()

	d := newDirs(t) // store exists but is empty: no species/ and no index.json

	if _, _, err := newSyncer(t, newUpstream(t, wren), d).Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if got := taxa(mustLoadStore(t, d.store)); !slices.Equal(got, []int{1001}) {
		t.Fatalf("store holds taxa %v, want [1001]", got)
	}
}

func TestSyncIsANoOpWhenNothingIsNew(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch)
	d := newDirs(t)
	syncer := newSyncer(t, source, d)

	if _, _, err := syncer.Sync(context.Background()); err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}

	before := readTree(t, d.store)
	source.fetched = nil

	gotCommit, added, err := syncer.Sync(context.Background())
	if err != nil || gotCommit != commit || added != 0 {
		t.Fatalf("second Sync() = %q, %d, %v; want %q, 0, nil", gotCommit, added, err, commit)
	}

	if n := source.fetchedSpecies(); n != 0 {
		t.Errorf("second Sync() fetched %d species files, want none", n)
	}

	if after := readTree(t, d.store); !equalMaps(before, after) {
		t.Error("second Sync() changed the store")
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}

func TestSyncFetchesOnlyWhatIsMissing(t *testing.T) {
	t.Parallel()

	d := newDirs(t)

	if _, _, err := newSyncer(t, newUpstream(t, wren), d).Sync(context.Background()); err != nil {
		t.Fatalf("seeding Sync() error = %v", err)
	}

	source := newUpstream(t, wren, finch, bunting)

	_, added, err := newSyncer(t, source, d).Sync(context.Background())
	if err != nil || added != 2 {
		t.Fatalf("Sync() = %d, %v; want 2 added", added, err)
	}

	for _, p := range source.fetched {
		if strings.Contains(p, wren.dir()) {
			t.Errorf("fetched %s, a species the store already holds", p)
		}
	}

	if got := taxa(mustLoadStore(t, d.store)); !slices.Equal(got, []int{1001, 1002, 1003}) {
		t.Fatalf("store holds taxa %v, want [1001 1002 1003]", got)
	}
}

func TestSyncKeepsSpeciesUpstreamNoLongerLists(t *testing.T) {
	t.Parallel()

	d := newDirs(t)

	if _, _, err := newSyncer(t, newUpstream(t, wren, finch), d).Sync(context.Background()); err != nil {
		t.Fatalf("seeding Sync() error = %v", err)
	}

	if _, added, err := newSyncer(t, newUpstream(t, finch, bunting), d).Sync(context.Background()); err != nil || added != 1 {
		t.Fatalf("Sync() = %d, %v; want 1 added", added, err)
	}

	if got := taxa(mustLoadStore(t, d.store)); !slices.Equal(got, []int{1001, 1002, 1003}) {
		t.Fatalf("store holds taxa %v, want the wren kept alongside the new bunting", got)
	}
}

func TestSyncAcceptsSpeciesWithoutOptionalFiles(t *testing.T) {
	t.Parallel()

	bare := wren
	bare.Bare = true

	d := newDirs(t)

	if _, added, err := newSyncer(t, newUpstream(t, bare), d).Sync(context.Background()); err != nil || added != 1 {
		t.Fatalf("Sync() = %d, %v; want 1 added", added, err)
	}

	mustLoadStore(t, d.store)
}

func TestSyncRefusesToChangeASpeciesTheStoreHolds(t *testing.T) {
	t.Parallel()

	d := newDirs(t)

	if _, _, err := newSyncer(t, newUpstream(t, wren, finch), d).Sync(context.Background()); err != nil {
		t.Fatalf("seeding Sync() error = %v", err)
	}

	before := readTree(t, d.store)

	changed := wren
	changed.Seed = 99

	renamed := finch
	renamed.Slug = "renamed-finch"

	source := newUpstream(t, changed, renamed, bunting)

	_, _, err := newSyncer(t, source, d).Sync(context.Background())
	if !errors.Is(err, mirror.ErrUpstreamChanged) {
		t.Fatalf("Sync() error = %v, want ErrUpstreamChanged", err)
	}

	for _, want := range []string{wren.dir(), "1002"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}

	if after := readTree(t, d.store); !equalMaps(before, after) {
		t.Error("a refused sync changed the store, including the new species")
	}

	if n := source.fetchedSpecies(); n != 0 {
		t.Errorf("fetched %d species files before refusing; it should stop first", n)
	}
}

func TestSyncPublishesNothingWhenADownloadIsWrong(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		damage func(t *testing.T, source *fakeSource)
		want   error
	}{
		"portrait differs from the index": {
			damage: func(t *testing.T, s *fakeSource) {
				s.files["catalog/species/"+finch.dir()+"/portrait.png"] = pngBytes(t, 1200, 1600, 77)
			},
			want: mirror.ErrAssetMismatch,
		},
		"display is missing": {
			damage: func(_ *testing.T, s *fakeSource) {
				delete(s.files, "catalog/species/"+finch.dir()+"/display.png")
			},
			want: upstream.ErrNotFound,
		},
		"manifest is missing": {
			damage: func(_ *testing.T, s *fakeSource) {
				delete(s.files, "catalog/species/"+finch.dir()+"/manifest.json")
			},
			want: upstream.ErrNotFound,
		},
		"manifest disagrees with the index": {
			damage: func(t *testing.T, s *fakeSource) {
				other := finch
				other.Common = "Not The Finch"
				s.files["catalog/species/"+finch.dir()+"/manifest.json"] = speciesFiles(t, other)["species/"+finch.dir()+"/manifest.json"]
			},
			want: mirror.ErrInvalidUpstreamIndex,
		},
		"species fails the catalog rules": {
			damage: func(t *testing.T, s *fakeSource) {
				// The manifest and its review stay consistent but the review did not pass.
				var manifest map[string]any
				if err := json.Unmarshal(s.files["catalog/species/"+finch.dir()+"/manifest.json"], &manifest); err != nil {
					t.Fatalf("decoding manifest: %v", err)
				}

				manifest["quality_review"].(map[string]any)["passed"] = false
				s.files["catalog/species/"+finch.dir()+"/manifest.json"] = mustJSON(t, manifest)
				s.files["catalog/species/"+finch.dir()+"/quality-review.json"] = mustJSON(t, manifest["quality_review"])
			},
			want: catalog.ErrReviewNotPublishable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDirs(t)

			if _, _, err := newSyncer(t, newUpstream(t, wren), d).Sync(context.Background()); err != nil {
				t.Fatalf("seeding Sync() error = %v", err)
			}

			before := readTree(t, d.store)

			source := newUpstream(t, wren, finch, bunting)
			tt.damage(t, source)

			_, _, err := newSyncer(t, source, d).Sync(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Sync() error = %v, want %v", err, tt.want)
			}

			if after := readTree(t, d.store); !equalMaps(before, after) {
				t.Error("a failed sync changed the store, including the species that were fine")
			}

			for name := range readTree(t, d.work) {
				if strings.Contains(name, ".partial") {
					t.Errorf("work directory kept the unfinished download %q after a failure", name)
				}
			}
		})
	}
}

func TestSyncRollsBackWhenTheWholeStoreIsInvalid(t *testing.T) {
	t.Parallel()

	d := newDirs(t)

	// A broken local species makes the final rebuild of the store's index fail.
	broken := filepath.Join(d.store, "species", "9999-broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatalf("creating broken species: %v", err)
	}

	if err := os.WriteFile(filepath.Join(broken, "manifest.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("writing broken manifest: %v", err)
	}

	before := readTree(t, d.store)

	_, _, err := newSyncer(t, newUpstream(t, wren), d).Sync(context.Background())

	var speciesErr *catalog.SpeciesError
	if !errors.As(err, &speciesErr) || speciesErr.Dir != "9999-broken" {
		t.Fatalf("Sync() error = %v, want the broken species reported", err)
	}

	if after := readTree(t, d.store); !equalMaps(before, after) {
		t.Error("the new species was left in the store after the rebuild failed")
	}

	if _, statErr := os.Stat(filepath.Join(d.store, "species", wren.dir())); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("stat of the rolled-back species = %v, want not exist", statErr)
	}
}

func TestSyncRejectsAnInvalidUpstreamIndex(t *testing.T) {
	t.Parallel()

	valid := func(t *testing.T) map[string]any {
		t.Helper()

		var index map[string]any
		if err := json.Unmarshal(newUpstream(t, wren, finch).files["catalog/index.json"], &index); err != nil {
			t.Fatalf("decoding index: %v", err)
		}

		return index
	}

	tests := map[string]func(index map[string]any){
		"unsupported schema": func(i map[string]any) { i["schema_version"] = 2 },
		"unknown top field":  func(i map[string]any) { i["extra"] = true },
		"unknown entry field": func(i map[string]any) {
			i["species"].([]any)[0].(map[string]any)["observation_count"] = 3
		},
		"duplicate taxon": func(i map[string]any) {
			s := i["species"].([]any)
			i["species"] = append(s, s[0])
		},
		"path escapes its directory": func(i map[string]any) {
			i["species"].([]any)[0].(map[string]any)["portrait_path"] = "species/../../etc/passwd"
		},
		"path names another species": func(i map[string]any) {
			i["species"].([]any)[0].(map[string]any)["display_path"] = "species/" + finch.dir() + "/display.png"
		},
		"slug is not a slug": func(i map[string]any) {
			i["species"].([]any)[0].(map[string]any)["slug"] = "Bad Slug"
		},
		"checksum is malformed": func(i map[string]any) {
			i["species"].([]any)[0].(map[string]any)["portrait_sha256"] = "XYZ"
		},
		"taxon id is zero": func(i map[string]any) {
			i["species"].([]any)[0].(map[string]any)["taxon_id"] = 0
		},
	}

	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			index := valid(t)
			edit(index)

			source := newUpstream(t, wren, finch)
			source.files["catalog/index.json"] = mustJSON(t, index)

			d := newDirs(t)

			_, _, err := newSyncer(t, source, d).Sync(context.Background())
			if !errors.Is(err, mirror.ErrInvalidUpstreamIndex) {
				t.Fatalf("Sync() error = %v, want ErrInvalidUpstreamIndex", err)
			}

			if n := source.fetchedSpecies(); n != 0 {
				t.Errorf("fetched %d species files from an invalid index", n)
			}
		})
	}

	t.Run("not JSON", func(t *testing.T) {
		t.Parallel()

		source := newUpstream(t, wren)
		source.files["catalog/index.json"] = []byte("[")

		if _, _, err := newSyncer(t, source, newDirs(t)).Sync(context.Background()); !errors.Is(err, mirror.ErrInvalidUpstreamIndex) {
			t.Fatalf("Sync() error = %v, want ErrInvalidUpstreamIndex", err)
		}
	})
}

func TestSyncPropagatesSourceFailures(t *testing.T) {
	t.Parallel()

	t.Run("ref does not resolve", func(t *testing.T) {
		t.Parallel()

		source := newUpstream(t, wren)
		source.resolveErr = upstream.ErrNotFound

		if _, _, err := newSyncer(t, source, newDirs(t)).Sync(context.Background()); !errors.Is(err, upstream.ErrNotFound) {
			t.Fatalf("Sync() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("index is missing", func(t *testing.T) {
		t.Parallel()

		source := newUpstream(t, wren)
		delete(source.files, "catalog/index.json")

		if _, _, err := newSyncer(t, source, newDirs(t)).Sync(context.Background()); !errors.Is(err, upstream.ErrNotFound) {
			t.Fatalf("Sync() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("context is canceled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, _, err := newSyncer(t, newUpstream(t, wren), newDirs(t)).Sync(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Sync() error = %v, want context.Canceled", err)
		}
	})
}

func TestSyncRefusesToRunTwiceAtOnce(t *testing.T) {
	t.Parallel()

	d := newDirs(t)

	if err := os.MkdirAll(d.work, 0o755); err != nil {
		t.Fatalf("creating work directory: %v", err)
	}

	held, err := fsutil.AcquireLock(filepath.Join(d.work, "sync.lock"))
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}

	defer func() {
		if err := held.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}()

	source := newUpstream(t, wren)

	if _, _, err := newSyncer(t, source, d).Sync(context.Background()); !errors.Is(err, fsutil.ErrLocked) {
		t.Fatalf("Sync() error = %v, want ErrLocked", err)
	}

	if len(source.fetched) != 0 {
		t.Errorf("fetched %v while another sync held the lock", source.fetched)
	}
}

func TestNewSyncerRequiresEveryArgument(t *testing.T) {
	t.Parallel()

	if _, err := mirror.NewSyncer(nil, nil, "", "", "", ""); !errors.Is(err, mirror.ErrInvalidSyncer) {
		t.Fatalf("NewSyncer() error = %v, want ErrInvalidSyncer", err)
	} else {
		for _, name := range []string{"source", "progress", "repo", "ref", "store", "work"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name %s", err, name)
			}
		}
	}

	if _, err := mirror.NewSyncer(&fakeSource{}, &recordingProgress{}, "a/b", "main", "/s", "/w"); err != nil {
		t.Fatalf("NewSyncer() with every argument error = %v", err)
	}
}

// stagedSpecies lists the complete species a failed sync left in staging.
func stagedSpecies(t *testing.T, work string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(work, "stage-"+commit, "species"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		t.Fatalf("reading staging: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	slices.Sort(names)

	return names
}

func speciesPath(f fixture, name string) string {
	return path.Join("catalog", "species", f.dir(), name)
}

func (f *fakeSource) fetchedAny(prefix string) bool {
	for _, p := range f.fetched {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}

	return false
}

func TestSyncResumesAfterAFailureWithoutRefetchingVerifiedSpecies(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch, bunting)
	d := newDirs(t)

	broken := speciesPath(bunting, "display.png")
	saved := source.files[broken]
	delete(source.files, broken)

	if _, _, err := newSyncer(t, source, d).Sync(context.Background()); !errors.Is(err, upstream.ErrNotFound) {
		t.Fatalf("first Sync() error = %v, want ErrNotFound", err)
	}

	if got := stagedSpecies(t, d.work); !slices.Equal(got, []string{wren.dir(), finch.dir()}) {
		t.Fatalf("staging holds %v after the failure, want the two verified species", got)
	}

	if got := readTree(t, d.store); len(got) != 0 {
		t.Fatalf("the store changed during a failed sync: %v", got)
	}

	source.files[broken] = saved
	source.fetched = nil

	if _, added, err := newSyncer(t, source, d).Sync(context.Background()); err != nil || added != 3 {
		t.Fatalf("second Sync() = %d, %v; want 3 added", added, err)
	}

	for _, f := range []fixture{wren, finch} {
		if source.fetchedAny(path.Join("catalog", "species", f.dir())) {
			t.Errorf("the second sync refetched %s, which was already staged and verified", f.dir())
		}
	}

	if !source.fetchedAny(path.Join("catalog", "species", bunting.dir())) {
		t.Error("the second sync did not fetch the species that had failed")
	}

	if got := taxa(mustLoadStore(t, d.store)); !slices.Equal(got, []int{1001, 1002, 1003}) {
		t.Fatalf("store holds taxa %v, want all three", got)
	}

	for name := range readTree(t, d.work) {
		if name != "upstream.json" && name != "sync.lock" {
			t.Errorf("work directory kept %q after a successful sync", name)
		}
	}
}

func TestSyncReportsEachStepAndWhichSpeciesWereReused(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch, bunting)
	d := newDirs(t)

	broken := speciesPath(bunting, "display.png")
	saved := source.files[broken]
	delete(source.files, broken)

	first := &recordingProgress{}
	if _, _, err := newSyncerReporting(t, source, first, d).Sync(context.Background()); err == nil {
		t.Fatal("first Sync() error = nil, want failure")
	}

	wantFirst := []string{
		"resolved " + commit,
		"planned 3 3",
		"staged 1/3 " + wren.dir() + " reused=false",
		"staged 2/3 " + finch.dir() + " reused=false",
	}
	if !slices.Equal(first.events, wantFirst) {
		t.Fatalf("first sync reported %q, want %q", first.events, wantFirst)
	}

	source.files[broken] = saved

	second := &recordingProgress{}
	if _, _, err := newSyncerReporting(t, source, second, d).Sync(context.Background()); err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}

	wantSecond := []string{
		"resolved " + commit,
		"planned 3 3",
		"staged 1/3 " + wren.dir() + " reused=true",
		"staged 2/3 " + finch.dir() + " reused=true",
		"staged 3/3 " + bunting.dir() + " reused=false",
		"validating",
		"publishing",
	}
	if !slices.Equal(second.events, wantSecond) {
		t.Fatalf("second sync reported %q, want %q", second.events, wantSecond)
	}
}

func TestSyncRefetchesAStagedSpeciesThatNoLongerMatches(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch)
	d := newDirs(t)

	broken := speciesPath(finch, "display.png")
	saved := source.files[broken]
	delete(source.files, broken)

	if _, _, err := newSyncer(t, source, d).Sync(context.Background()); err == nil {
		t.Fatal("first Sync() error = nil, want failure")
	}

	staged := filepath.Join(d.work, "stage-"+commit, "species", wren.dir(), "portrait.png")
	if err := os.WriteFile(staged, []byte("corrupted on disk"), 0o644); err != nil {
		t.Fatalf("corrupting staged file: %v", err)
	}

	source.files[broken] = saved
	source.fetched = nil

	if _, added, err := newSyncer(t, source, d).Sync(context.Background()); err != nil || added != 2 {
		t.Fatalf("second Sync() = %d, %v; want 2 added", added, err)
	}

	if !source.fetchedAny(path.Join("catalog", "species", wren.dir())) {
		t.Error("a staged species whose file no longer matches was not downloaded again")
	}

	mustLoadStore(t, d.store)
}

func TestSyncRemovesUnfinishedDownloadsAndStaleStaging(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch)
	d := newDirs(t)

	stage := filepath.Join(d.work, "stage-"+commit, "species")
	staleCommit := filepath.Join(d.work, "stage-"+strings.Repeat("a", 40), "species", "9999-old")
	legacy := filepath.Join(d.work, "stage-123456789")

	for _, dir := range []string{filepath.Join(stage, finch.dir()+".partial"), staleCommit, legacy} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}

		if err := os.WriteFile(filepath.Join(dir, "leftover"), []byte("x"), 0o644); err != nil {
			t.Fatalf("writing leftover: %v", err)
		}
	}

	unrelated := filepath.Join(d.work, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("writing unrelated file: %v", err)
	}

	if _, added, err := newSyncer(t, source, d).Sync(context.Background()); err != nil || added != 2 {
		t.Fatalf("Sync() = %d, %v; want 2 added", added, err)
	}

	left := readTree(t, d.work)
	for name := range left {
		if strings.HasPrefix(name, "stage-") {
			t.Errorf("work directory kept staging %q", name)
		}
	}

	if left["notes.txt"] != "keep me" {
		t.Error("sync removed a file in the work directory that is not its staging")
	}
}

func TestSyncDropsStagedSpeciesTheStoreNoLongerLacks(t *testing.T) {
	t.Parallel()

	source := newUpstream(t, wren, finch, bunting)
	d := newDirs(t)

	broken := speciesPath(bunting, "display.png")
	saved := source.files[broken]
	delete(source.files, broken)

	if _, _, err := newSyncer(t, source, d).Sync(context.Background()); err == nil {
		t.Fatal("first Sync() error = nil, want failure")
	}

	// Another sync, with its own work directory, adds the wren to the store.
	other := dirs{store: d.store, work: filepath.Join(filepath.Dir(d.work), "other-work")}
	if _, _, err := newSyncer(t, newUpstream(t, wren), other).Sync(context.Background()); err != nil {
		t.Fatalf("other Sync() error = %v", err)
	}

	source.files[broken] = saved
	source.fetched = nil

	if _, added, err := newSyncer(t, source, d).Sync(context.Background()); err != nil || added != 2 {
		t.Fatalf("Sync() = %d, %v; want 2 added (the wren is already in the store)", added, err)
	}

	if got := taxa(mustLoadStore(t, d.store)); !slices.Equal(got, []int{1001, 1002, 1003}) {
		t.Fatalf("store holds taxa %v, want all three", got)
	}
}

func TestSyncRejectsACommitThatIsNotAFullSHA(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"main", "../../etc", "", "8bc988b", strings.ToUpper(commit)} {
		source := newUpstream(t, wren)
		source.commit = bad

		d := newDirs(t)

		if _, _, err := newSyncer(t, source, d).Sync(context.Background()); !errors.Is(err, mirror.ErrInvalidCommit) {
			t.Errorf("Sync() with commit %q error = %v, want ErrInvalidCommit", bad, err)
		}

		if len(source.fetched) != 0 {
			t.Errorf("fetched %v with the invalid commit %q", source.fetched, bad)
		}
	}
}
