package catalog_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

// writeTree copies an in-memory catalog onto disk under a new temporary
// directory, including directories and symlinks.
func writeTree(t *testing.T, tree fstest.MapFS) string {
	t.Helper()

	dir := t.TempDir()

	for name, file := range tree {
		path := filepath.Join(dir, filepath.FromSlash(name))

		switch {
		case file.Mode&fs.ModeSymlink != 0:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("creating directory for %s: %v", name, err)
			}

			if err := os.Symlink(string(file.Data), path); err != nil {
				t.Fatalf("creating symlink %s: %v", name, err)
			}
		case file.Mode.IsDir():
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatalf("creating directory %s: %v", name, err)
			}
		default:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("creating directory for %s: %v", name, err)
			}

			if err := os.WriteFile(path, file.Data, 0o644); err != nil {
				t.Fatalf("writing %s: %v", name, err)
			}
		}
	}

	return dir
}

// loadFS writes an in-memory catalog to disk and loads it through DirLoader,
// the package's one entry point.
func loadFS(t *testing.T, tree fstest.MapFS) (*catalog.Catalog, error) {
	t.Helper()

	loader, err := catalog.NewDirLoader(writeTree(t, tree))
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	return loader.Load()
}

func TestDirLoaderLoadsACatalogFromDisk(t *testing.T) {
	t.Parallel()

	dir := writeTree(t, build(t, []speciesFixture{wren, finch}, nil))

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	got, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if n := len(got.Species()); n != 2 {
		t.Fatalf("Load() loaded %d species, want 2", n)
	}
}

func TestNewDirLoaderRequiresADirectory(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", "   "} {
		if _, err := catalog.NewDirLoader(dir); !errors.Is(err, catalog.ErrNoDirectory) {
			t.Errorf("NewDirLoader(%q) error = %v, want ErrNoDirectory", dir, err)
		}
	}
}

func TestDirLoaderReportsProblems(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dir  func(t *testing.T) string
		want error
	}{
		"missing directory": {
			dir:  func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
			want: fs.ErrNotExist,
		},
		"empty directory": {
			dir:  func(t *testing.T) string { return t.TempDir() },
			want: catalog.ErrMissingFile,
		},
		"invalid catalog": {
			dir: func(t *testing.T) string {
				return writeTree(t, build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) {
					f.qualityReview()["passed"] = false
				}))
			},
			want: catalog.ErrReviewNotPublishable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loader, err := catalog.NewDirLoader(tt.dir(t))
			if err != nil {
				t.Fatalf("NewDirLoader() error = %v", err)
			}

			got, err := loader.Load()
			if !errors.Is(err, tt.want) {
				t.Fatalf("Load() error = %v, want %v", err, tt.want)
			}

			if got != nil {
				t.Fatalf("Load() = %v with an error, want no catalog", got)
			}
		})
	}
}

func TestDirLoaderRebuildIndexWritesTheCanonicalIndex(t *testing.T) {
	t.Parallel()

	tree := build(t, []speciesFixture{finch, wren}, nil)
	want := tree["index.json"].Data

	delete(tree, "index.json")

	dir := writeTree(t, tree)

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	got, err := loader.RebuildIndex()
	if err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}

	if n := len(got.Species()); n != 2 {
		t.Fatalf("RebuildIndex() reported %d species, want 2", n)
	}

	written, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil || string(written) != string(want) {
		t.Fatalf("index.json = %q, %v; want the canonical %q", written, err, want)
	}

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load() after RebuildIndex error = %v", err)
	}
}

func TestDirLoaderRebuildIndexReplacesAStaleIndex(t *testing.T) {
	t.Parallel()

	tree := build(t, []speciesFixture{wren, finch}, nil)
	tree["index.json"] = &fstest.MapFile{Data: []byte(`{"generated_at": null, "schema_version": 1, "species": []}` + "\n")}

	loader, err := catalog.NewDirLoader(writeTree(t, tree))
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := loader.Load(); !errors.Is(err, catalog.ErrIndexMismatch) {
		t.Fatalf("Load() with a stale index error = %v, want ErrIndexMismatch", err)
	}

	if _, err := loader.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load() after RebuildIndex error = %v", err)
	}
}

func TestDirLoaderRebuildIndexWritesAnEmptyIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := loader.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}

	written, err := os.ReadFile(filepath.Join(dir, "index.json"))
	want := "{\n  \"generated_at\": null,\n  \"schema_version\": 1,\n  \"species\": []\n}\n"

	if err != nil || string(written) != want {
		t.Fatalf("index.json = %q, %v; want %q", written, err, want)
	}
}

func TestDirLoaderRebuildIndexRefusesAnInvalidTree(t *testing.T) {
	t.Parallel()

	staleIndex := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"

	tests := map[string]struct {
		tree fstest.MapFS
		want error
	}{
		"invalid species": {
			tree: build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) { f.qualityReview()["passed"] = false }),
			want: catalog.ErrReviewNotPublishable,
		},
		"unexpected root entry": {
			tree: func() fstest.MapFS {
				tree := build(t, []speciesFixture{wren}, nil)
				tree["notes.txt"] = &fstest.MapFile{Data: []byte("x")}

				return tree
			}(),
			want: catalog.ErrUnexpectedFile,
		},
		"missing species root": {
			tree: fstest.MapFS{"index.json": &fstest.MapFile{Data: []byte(staleIndex)}},
			want: catalog.ErrMissingFile,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tt.tree["index.json"] = &fstest.MapFile{Data: []byte(staleIndex)}
			dir := writeTree(t, tt.tree)

			loader, err := catalog.NewDirLoader(dir)
			if err != nil {
				t.Fatalf("NewDirLoader() error = %v", err)
			}

			got, err := loader.RebuildIndex()
			if !errors.Is(err, tt.want) {
				t.Fatalf("RebuildIndex() error = %v, want %v", err, tt.want)
			}

			if got != nil {
				t.Fatalf("RebuildIndex() = %v with an error, want no catalog", got)
			}

			after, readErr := os.ReadFile(filepath.Join(dir, "index.json"))
			if readErr != nil || string(after) != staleIndex {
				t.Fatalf("index.json = %q, %v after a refused rebuild; want it untouched", after, readErr)
			}
		})
	}
}

func TestDirLoaderReadAssetReturnsAFilesBytes(t *testing.T) {
	t.Parallel()

	dir := writeTree(t, build(t, []speciesFixture{wren}, nil))

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	got, err := loader.ReadAsset("species/1001-carolina-wren/display.png")
	if err != nil || string(got) != string(displayPNG) {
		t.Fatalf("ReadAsset() = %d bytes, %v; want the display image (%d bytes)", len(got), err, len(displayPNG))
	}
}

func TestDirLoaderReadAssetRefusesUnsafeNames(t *testing.T) {
	t.Parallel()

	dir := writeTree(t, build(t, []speciesFixture{wren}, nil))

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	for _, name := range []string{"", ".", "..", "../x.png", "/etc/passwd", "species//x.png", "species/../index.json", `species\x.png`, "species/./x.png"} {
		if _, err := loader.ReadAsset(name); !errors.Is(err, catalog.ErrInvalidAssetName) {
			t.Errorf("ReadAsset(%q) error = %v, want ErrInvalidAssetName", name, err)
		}
	}
}

func TestDirLoaderReadAssetRefusesNonRegularFilesAndMissingOnes(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("writing outside file: %v", err)
	}

	tree := build(t, []speciesFixture{wren}, nil)
	tree["link-out.png"] = &fstest.MapFile{Data: []byte(filepath.Join(outside, "secret.png")), Mode: fs.ModeSymlink}
	tree["link-in.png"] = &fstest.MapFile{Data: []byte("species/1001-carolina-wren/display.png"), Mode: fs.ModeSymlink}

	loader, err := catalog.NewDirLoader(writeTree(t, tree))
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	for _, name := range []string{"link-out.png", "link-in.png", "species", "species/1001-carolina-wren"} {
		if _, err := loader.ReadAsset(name); !errors.Is(err, catalog.ErrNotRegularFile) {
			t.Errorf("ReadAsset(%q) error = %v, want ErrNotRegularFile", name, err)
		}
	}

	if _, err := loader.ReadAsset("species/1001-carolina-wren/absent.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadAsset() of a missing file error = %v, want fs.ErrNotExist", err)
	}
}

func TestDirLoaderReadAssetBoundsTheFileSize(t *testing.T) {
	t.Parallel()

	// The documented per-file limit is 32 MiB.
	const limit = 32 << 20

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.png"), make([]byte, limit+1), 0o644); err != nil {
		t.Fatalf("writing big file: %v", err)
	}

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := loader.ReadAsset("big.png"); err == nil {
		t.Fatal("ReadAsset() of an oversized file error = nil, want failure")
	}
}

func TestDirLoaderIndexVersionChangesWhenTheIndexDoes(t *testing.T) {
	t.Parallel()

	tree := build(t, []speciesFixture{wren, finch}, nil)
	dir := writeTree(t, tree)

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	first, err := loader.IndexVersion()
	if err != nil {
		t.Fatalf("IndexVersion() error = %v", err)
	}

	again, err := loader.IndexVersion()
	if err != nil || again != first {
		t.Fatalf("IndexVersion() of an unchanged index = %q, %v; want %q", again, err, first)
	}

	// Removing a species and rebuilding changes the index file.
	if err := os.RemoveAll(filepath.Join(dir, "species", "1002-house-finch")); err != nil {
		t.Fatalf("removing species: %v", err)
	}

	if _, err := loader.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}

	changed, err := loader.IndexVersion()
	if err != nil || changed == first {
		t.Fatalf("IndexVersion() after a rebuild = %q, %v; want something other than %q", changed, err, first)
	}
}

func TestDirLoaderIndexVersionReportsAMissingIndex(t *testing.T) {
	t.Parallel()

	loader, err := catalog.NewDirLoader(t.TempDir())
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := loader.IndexVersion(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("IndexVersion() error = %v, want fs.ErrNotExist", err)
	}
}

func TestCatalogGeneratedAtIsTheLatestApproval(t *testing.T) {
	t.Parallel()

	loader, err := catalog.NewDirLoader(writeTree(t, build(t, []speciesFixture{wren, finch, bunting}, nil)))
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	got, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if at, ok := got.GeneratedAt(); !ok || at != finch.ApprovedAt {
		t.Fatalf("GeneratedAt() = %q, %t; want %q, true", at, ok, finch.ApprovedAt)
	}

	empty := build(t, nil, nil)
	empty["species"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}

	emptyLoader, err := catalog.NewDirLoader(writeTree(t, empty))
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	loaded, err := emptyLoader.Load()
	if err != nil {
		t.Fatalf("Load() of an empty catalog error = %v", err)
	}

	if at, ok := loaded.GeneratedAt(); ok || at != "" {
		t.Fatalf("GeneratedAt() of an empty catalog = %q, %t; want empty, false", at, ok)
	}
}
