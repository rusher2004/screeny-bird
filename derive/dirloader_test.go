package derive_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/derive"
)

func newLoader(t *testing.T, dir string) derive.DirLoader {
	t.Helper()

	loader, err := derive.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	return loader
}

func TestNewDirLoaderRequiresADirectory(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", "  "} {
		if _, err := derive.NewDirLoader(dir); !errors.Is(err, derive.ErrNoDirectory) {
			t.Errorf("NewDirLoader(%q) error = %v, want ErrNoDirectory", dir, err)
		}
	}
}

func TestDirLoaderLoadsTheIndexTheDeriverWrote(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	if _, err := newDeriver(t, newStore(t, fixtureSpecies...), &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	idx, err := newLoader(t, out).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if idx.Width != width || idx.Height != height || len(idx.Species) != 3 {
		t.Fatalf("Load() = %+v, want %dx%d with three species", idx, width, height)
	}

	if want := fmt.Sprintf("species/%s/%dx%d.png", fixtureSpecies[0], width, height); idx.Species[0].Path != want || idx.Species[0].TaxonID != 1001 {
		t.Errorf("first entry = %+v, want taxon 1001 at %q", idx.Species[0], want)
	}
}

func TestDirLoaderRejectsAnInconsistentIndex(t *testing.T) {
	t.Parallel()

	const good = `{"height":180,"schema_version":1,"species":[%s],"width":320}`

	entry := func(path, sum, slug string) string {
		return fmt.Sprintf(`{"path":%q,"sha256":%q,"slug":%q,"taxon_id":1001}`, path, sum, slug)
	}

	sum := strings.Repeat("a", 64)
	ok := "species/1001-carolina-wren/320x180.png"

	tests := map[string]string{
		"not json":         "{",
		"unknown field":    `{"extra":1}`,
		"wrong schema":     `{"height":180,"schema_version":2,"species":[],"width":320}`,
		"zero size":        `{"height":0,"schema_version":1,"species":[],"width":320}`,
		"oversized":        `{"height":180,"schema_version":1,"species":[],"width":99999}`,
		"path elsewhere":   fmt.Sprintf(good, entry("../../etc/passwd", sum, "carolina-wren")),
		"wrong size path":  fmt.Sprintf(good, entry("species/1001-carolina-wren/640x360.png", sum, "carolina-wren")),
		"bad slug":         fmt.Sprintf(good, entry("species/1001-../320x180.png", sum, "..")),
		"short checksum":   fmt.Sprintf(good, entry(ok, "abc", "carolina-wren")),
		"duplicate taxa":   fmt.Sprintf(good, entry(ok, sum, "carolina-wren")+","+entry(ok, sum, "carolina-wren")),
		"zero taxon":       `{"height":180,"schema_version":1,"species":[{"path":"species/0-x/320x180.png","sha256":"` + sum + `","slug":"x","taxon_id":0}],"width":320}`,
		"negative taxon":   `{"height":180,"schema_version":1,"species":[{"path":"species/-5-x/320x180.png","sha256":"` + sum + `","slug":"x","taxon_id":-5}],"width":320}`,
		"uppercase digest": fmt.Sprintf(good, entry(ok, strings.Repeat("A", 64), "carolina-wren")),
	}

	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(content), 0o644); err != nil {
				t.Fatalf("writing index: %v", err)
			}

			if _, err := newLoader(t, dir).Load(); !errors.Is(err, derive.ErrInvalidIndex) {
				t.Fatalf("Load() error = %v, want ErrInvalidIndex", err)
			}
		})
	}

	if _, err := newLoader(t, t.TempDir()).Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load() without an index error = %v, want fs.ErrNotExist", err)
	}
}

func TestDirLoaderIndexVersionChangesWhenTheIndexIsRewritten(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	loader := newLoader(t, out)

	if _, err := loader.IndexVersion(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("IndexVersion() without an index error = %v, want fs.ErrNotExist", err)
	}

	store := newStore(t, fixtureSpecies[:1]...)
	if _, err := newDeriver(t, store, &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	first, err := loader.IndexVersion()
	if err != nil {
		t.Fatalf("IndexVersion() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(out, "index.json"), []byte(`{"height":180,"schema_version":1,"species":[],"width":320}`+"\n"), 0o644); err != nil {
		t.Fatalf("rewriting index: %v", err)
	}

	if second, err := loader.IndexVersion(); err != nil || second == first {
		t.Fatalf("IndexVersion() = %q, %v after a rewrite; want a different token than %q", second, err, first)
	}
}

func TestDirLoaderReadsOnlyPlainFilesInsideItsDirectory(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	if _, err := newDeriver(t, newStore(t, fixtureSpecies[:1]...), &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	loader := newLoader(t, out)
	idx, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	data, err := loader.ReadAsset(idx.Species[0].Path)
	if err != nil {
		t.Fatalf("ReadAsset() error = %v", err)
	}

	if digest := sha256.Sum256(data); hex.EncodeToString(digest[:]) != idx.Species[0].SHA256 {
		t.Error("the image does not match its recorded checksum")
	}

	for _, name := range []string{"../index.json", "/etc/passwd", "species/../../x", "."} {
		if _, err := loader.ReadAsset(name); !errors.Is(err, catalog.ErrInvalidAssetName) {
			t.Errorf("ReadAsset(%q) error = %v, want ErrInvalidAssetName", name, err)
		}
	}

	link := filepath.Join(out, "species", fixtureSpecies[0], "link.png")
	if err := os.Symlink(filepath.Join(out, idx.Species[0].Path), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := loader.ReadAsset("species/" + fixtureSpecies[0] + "/link.png"); err == nil {
		t.Error("ReadAsset() followed a symlink")
	}
}

func TestDirLoaderRefusesASymlinkedOrOversizedIndex(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	if _, err := newDeriver(t, newStore(t, fixtureSpecies[:1]...), &recordingProgress{}, out).Derive(context.Background()); err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	loader := newLoader(t, out)
	index := filepath.Join(out, "index.json")

	real := filepath.Join(out, "real-index.json")
	if err := os.Rename(index, real); err != nil {
		t.Fatalf("moving index: %v", err)
	}

	if err := os.Symlink(real, index); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := loader.Load(); err == nil {
		t.Error("Load() followed a symlinked index")
	}

	if _, err := loader.IndexVersion(); !errors.Is(err, catalog.ErrNotRegularFile) {
		t.Errorf("IndexVersion() of a symlinked index error = %v, want ErrNotRegularFile", err)
	}

	if err := os.Remove(index); err != nil {
		t.Fatalf("removing link: %v", err)
	}

	if err := os.WriteFile(index, make([]byte, 33<<20), 0o644); err != nil {
		t.Fatalf("writing oversized index: %v", err)
	}

	if _, err := loader.Load(); err == nil {
		t.Error("Load() read an index over the size limit")
	}
}
