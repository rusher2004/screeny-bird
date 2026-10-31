package upstream

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// fetcher is what a Retrier wraps: anything that resolves a ref and fetches
// files, such as a Source.
type fetcher interface {
	ResolveRef(ctx context.Context) (string, error)
	Fetch(ctx context.Context, commit, filePath string) ([]byte, error)
}

// sleeper waits between attempts, so tests never wait on a real clock.
type sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Retrier retries the transient failures of the source it wraps, a bounded
// number of times with a fixed wait between attempts. A failure that is not
// marked ErrTransient is returned at once.
type Retrier struct {
	inner    fetcher
	attempts int
	delay    time.Duration
	sleeper  sleeper
}

// NewRetrier returns a retrier that makes up to attempts tries per request,
// waiting delay between them. attempts counts the first try, so 1 means no
// retries. It reports every invalid argument together.
func NewRetrier(inner fetcher, attempts int, delay time.Duration, sleeper sleeper) (Retrier, error) {
	var problems []error

	if inner == nil {
		problems = append(problems, errors.New("source is required"))
	}

	if attempts < 1 {
		problems = append(problems, fmt.Errorf("attempts must be at least 1, got %d", attempts))
	}

	if delay <= 0 {
		problems = append(problems, fmt.Errorf("delay must be positive, got %v", delay))
	}

	if sleeper == nil {
		problems = append(problems, errors.New("sleeper is required"))
	}

	if err := errors.Join(problems...); err != nil {
		return Retrier{}, fmt.Errorf("creating retrier: %w: %w", ErrInvalidSource, err)
	}

	return Retrier{inner: inner, attempts: attempts, delay: delay, sleeper: sleeper}, nil
}

// ResolveRef resolves the ref, retrying transient failures.
func (r Retrier) ResolveRef(ctx context.Context) (string, error) {
	return attempt(ctx, r, func() (string, error) { return r.inner.ResolveRef(ctx) })
}

// Fetch fetches the file, retrying transient failures.
func (r Retrier) Fetch(ctx context.Context, commit, filePath string) ([]byte, error) {
	return attempt(ctx, r, func() ([]byte, error) { return r.inner.Fetch(ctx, commit, filePath) })
}

// attempt runs op until it succeeds, fails permanently, runs out of attempts or
// ctx ends.
func attempt[T any](ctx context.Context, r Retrier, op func() (T, error)) (T, error) {
	var zero T

	for n := 1; ; n++ {
		result, err := op()
		if err == nil {
			return result, nil
		}

		if !errors.Is(err, ErrTransient) || ctx.Err() != nil {
			return zero, fmt.Errorf("attempt %d of %d: %w", n, r.attempts, err)
		}

		if n >= r.attempts {
			return zero, fmt.Errorf("giving up after %d attempts: %w", r.attempts, err)
		}

		if sleepErr := r.sleeper.Sleep(ctx, r.delay); sleepErr != nil {
			return zero, errors.Join(fmt.Errorf("waiting to retry after attempt %d: %w", n, sleepErr), err)
		}
	}
}
