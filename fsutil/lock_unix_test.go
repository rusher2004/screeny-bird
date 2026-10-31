//go:build unix

package fsutil_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/rusher2004/screeny-bird/fsutil"
)

func TestAcquireLockIsExclusiveUntilReleased(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sync.lock")

	first, err := fsutil.AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}

	if _, err := fsutil.AcquireLock(path); !errors.Is(err, fsutil.ErrLocked) {
		t.Fatalf("second AcquireLock() error = %v, want ErrLocked", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	again, err := fsutil.AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() after release error = %v", err)
	}

	if err := again.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestAcquireLockReportsAnUnusablePath(t *testing.T) {
	t.Parallel()

	if _, err := fsutil.AcquireLock(filepath.Join(t.TempDir(), "absent", "sync.lock")); err == nil {
		t.Fatal("AcquireLock() in a missing directory error = nil, want failure")
	}
}
