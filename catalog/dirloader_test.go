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

// writeTree copies an in-memory catalog onto disk under a new temporary directory.
func writeTree(t *testing.T, tree fstest.MapFS) string {
	t.Helper()

	dir := t.TempDir()

	for name, file := range tree {
		if file.Mode.IsDir() {
			continue
		}

		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating directory for %s: %v", name, err)
		}

		if err := os.WriteFile(path, file.Data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return dir
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
