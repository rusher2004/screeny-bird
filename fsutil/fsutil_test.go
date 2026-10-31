package fsutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rusher2004/screeny-bird/fsutil"
)

func TestWriteFileAtomicCreatesAndReplaces(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")

	for _, content := range []string{"first", "second, longer than the first"} {
		if err := fsutil.WriteFileAtomic(path, []byte(content), 0o640); err != nil {
			t.Fatalf("WriteFileAtomic(%q) error = %v", content, err)
		}

		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("file = %q, %v; want %q", got, err, content)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestWriteFileAtomicLeavesNoTemporaryFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "a.json"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading directory: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "a.json" {
		t.Fatalf("directory holds %v, want only a.json", entries)
	}
}

func TestWriteFileAtomicKeepsTheOldFileOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := fsutil.WriteFileAtomic(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	// A directory where the file should go makes the final rename fail.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("creating blocker: %v", err)
	}

	if err := fsutil.WriteFileAtomic(blocked, []byte("new"), 0o644); err == nil {
		t.Fatal("WriteFileAtomic() over a directory error = nil, want failure")
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "old" {
		t.Fatalf("untouched file = %q, %v; want old", got, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading directory: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("directory holds %v after a failed write, want only state.json and blocked", entries)
	}
}

func TestWriteFileAtomicRejectsAMissingDirectory(t *testing.T) {
	t.Parallel()

	if err := fsutil.WriteFileAtomic(filepath.Join(t.TempDir(), "absent", "f"), []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFileAtomic() into a missing directory error = nil, want failure")
	}
}
