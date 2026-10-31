//go:build unix

package fsutil

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrLocked reports a lock another process holds.
var ErrLocked = errors.New("lock is held by another process")

// Lock is an exclusive advisory lock on a file. The kernel releases it when the
// process exits, so a crash never leaves it stuck.
type Lock struct {
	file *os.File
}

// AcquireLock takes an exclusive lock on the file at path, creating it if
// needed. It does not wait: a lock already held yields ErrLocked.
func AcquireLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("locking %s: %w", path, ErrLocked), closeErr)
		}

		return nil, errors.Join(fmt.Errorf("locking %s: %w", path, err), closeErr)
	}

	return &Lock{file: file}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()

	if unlockErr != nil {
		return errors.Join(fmt.Errorf("unlocking %s: %w", l.file.Name(), unlockErr), closeErr)
	}

	if closeErr != nil {
		return fmt.Errorf("closing lock file %s: %w", l.file.Name(), closeErr)
	}

	return nil
}
