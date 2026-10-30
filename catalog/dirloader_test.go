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

	got, err := catalog.DirLoader{}.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	if n := len(got.Species()); n != 2 {
		t.Fatalf("LoadDir() loaded %d species, want 2", n)
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

			got, err := catalog.DirLoader{}.LoadDir(tt.dir(t))
			if !errors.Is(err, tt.want) {
				t.Fatalf("LoadDir() error = %v, want %v", err, tt.want)
			}

			if got != nil {
				t.Fatalf("LoadDir() = %v with an error, want no catalog", got)
			}
		})
	}
}
