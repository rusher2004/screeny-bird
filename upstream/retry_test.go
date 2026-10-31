package upstream_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rusher2004/screeny-bird/upstream"
)

type scriptedFetcher struct {
	// results are returned in order, one per call; the last repeats.
	results []error
	calls   int
	refs    int
}

func (f *scriptedFetcher) next() error {
	i := min(f.calls, len(f.results)-1)
	f.calls++

	return f.results[i]
}

func (f *scriptedFetcher) ResolveRef(context.Context) (string, error) {
	f.refs++

	if err := f.next(); err != nil {
		return "", err
	}

	return commit, nil
}

func (f *scriptedFetcher) Fetch(context.Context, string, string) ([]byte, error) {
	if err := f.next(); err != nil {
		return nil, err
	}

	return []byte("data"), nil
}

type fakeSleeper struct {
	slept []time.Duration
	err   error
}

func (f *fakeSleeper) Sleep(_ context.Context, d time.Duration) error {
	f.slept = append(f.slept, d)

	return f.err
}

var errStall = fmt.Errorf("stalled: %w", upstream.ErrTransient)

func newRetrier(t *testing.T, inner *scriptedFetcher, sleeper *fakeSleeper, attempts int) upstream.Retrier {
	t.Helper()

	retrier, err := upstream.NewRetrier(inner, attempts, 3*time.Second, sleeper)
	if err != nil {
		t.Fatalf("NewRetrier() error = %v", err)
	}

	return retrier
}

func TestRetrierReturnsAFirstSuccessWithoutWaiting(t *testing.T) {
	t.Parallel()

	inner, sleeper := &scriptedFetcher{results: []error{nil}}, &fakeSleeper{}

	got, err := newRetrier(t, inner, sleeper, 3).Fetch(context.Background(), commit, "catalog/index.json")
	if err != nil || string(got) != "data" || inner.calls != 1 || len(sleeper.slept) != 0 {
		t.Fatalf("Fetch() = %q, %v after %d calls and %d sleeps; want data after one call and no sleep", got, err, inner.calls, len(sleeper.slept))
	}
}

func TestRetrierRetriesTransientFailuresUntilOneSucceeds(t *testing.T) {
	t.Parallel()

	inner, sleeper := &scriptedFetcher{results: []error{errStall, errStall, nil}}, &fakeSleeper{}

	got, err := newRetrier(t, inner, sleeper, 3).Fetch(context.Background(), commit, "catalog/a.png")
	if err != nil || string(got) != "data" {
		t.Fatalf("Fetch() = %q, %v; want data after two stalls", got, err)
	}

	if inner.calls != 3 {
		t.Errorf("tried %d times, want 3", inner.calls)
	}

	if len(sleeper.slept) != 2 || sleeper.slept[0] != 3*time.Second || sleeper.slept[1] != 3*time.Second {
		t.Errorf("slept %v, want 3s before each of the two retries", sleeper.slept)
	}
}

func TestRetrierGivesUpAfterTheLastAttempt(t *testing.T) {
	t.Parallel()

	inner, sleeper := &scriptedFetcher{results: []error{errStall}}, &fakeSleeper{}

	_, err := newRetrier(t, inner, sleeper, 3).Fetch(context.Background(), commit, "catalog/a.png")
	if !errors.Is(err, upstream.ErrTransient) || !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("Fetch() error = %v, want the transient error and a note that 3 attempts failed", err)
	}

	if inner.calls != 3 || len(sleeper.slept) != 2 {
		t.Errorf("tried %d times and slept %d times, want 3 and 2 (no sleep after the last attempt)", inner.calls, len(sleeper.slept))
	}
}

func TestRetrierNeverRetriesAPermanentFailure(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"not found":    fmt.Errorf("gone: %w", upstream.ErrNotFound),
		"bad response": fmt.Errorf("teapot: %w", upstream.ErrBadResponse),
		"invalid":      fmt.Errorf("bad path: %w", upstream.ErrInvalidSource),
	} {
		inner, sleeper := &scriptedFetcher{results: []error{cause}}, &fakeSleeper{}

		_, err := newRetrier(t, inner, sleeper, 5).Fetch(context.Background(), commit, "catalog/a.png")
		if !errors.Is(err, cause) || inner.calls != 1 || len(sleeper.slept) != 0 {
			t.Errorf("%s: error %v after %d calls and %d sleeps; want the cause after one call and no sleep", name, err, inner.calls, len(sleeper.slept))
		}
	}
}

func TestRetrierWithOneAttemptDoesNotRetry(t *testing.T) {
	t.Parallel()

	inner, sleeper := &scriptedFetcher{results: []error{errStall}}, &fakeSleeper{}

	if _, err := newRetrier(t, inner, sleeper, 1).Fetch(context.Background(), commit, "catalog/a.png"); !errors.Is(err, upstream.ErrTransient) {
		t.Fatalf("Fetch() error = %v, want the transient error", err)
	}

	if inner.calls != 1 || len(sleeper.slept) != 0 {
		t.Errorf("tried %d times and slept %d times, want 1 and 0", inner.calls, len(sleeper.slept))
	}
}

func TestRetrierStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	t.Run("while waiting to retry", func(t *testing.T) {
		t.Parallel()

		inner, sleeper := &scriptedFetcher{results: []error{errStall}}, &fakeSleeper{err: context.Canceled}

		_, err := newRetrier(t, inner, sleeper, 5).Fetch(context.Background(), commit, "catalog/a.png")
		if !errors.Is(err, context.Canceled) || inner.calls != 1 {
			t.Fatalf("Fetch() error = %v after %d calls; want context.Canceled after one", err, inner.calls)
		}
	})

	t.Run("before a retry begins", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		inner := &scriptedFetcher{results: []error{errStall}}

		// The caller's context ends during the first attempt.
		sleeper := &fakeSleeper{}
		retrier := newRetrier(t, inner, sleeper, 5)

		cancel()

		_, err := retrier.Fetch(ctx, commit, "catalog/a.png")
		if err == nil || inner.calls > 1 {
			t.Fatalf("Fetch() error = %v after %d calls; want failure after at most one", err, inner.calls)
		}
	})
}

func TestRetrierRetriesRefResolutionToo(t *testing.T) {
	t.Parallel()

	inner, sleeper := &scriptedFetcher{results: []error{errStall, nil}}, &fakeSleeper{}

	got, err := newRetrier(t, inner, sleeper, 3).ResolveRef(context.Background())
	if err != nil || got != commit || inner.refs != 2 {
		t.Fatalf("ResolveRef() = %q, %v after %d tries; want %q after 2", got, err, inner.refs, commit)
	}
}

func TestNewRetrierRejectsBadArgumentsTogether(t *testing.T) {
	t.Parallel()

	_, err := upstream.NewRetrier(nil, 0, 0, nil)
	if !errors.Is(err, upstream.ErrInvalidSource) {
		t.Fatalf("NewRetrier() error = %v, want ErrInvalidSource", err)
	}

	for _, want := range []string{"source", "attempts", "delay", "sleeper"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	if _, err := upstream.NewRetrier(&scriptedFetcher{results: []error{nil}}, 1, time.Second, &fakeSleeper{}); err != nil {
		t.Fatalf("NewRetrier() with valid arguments error = %v", err)
	}
}
