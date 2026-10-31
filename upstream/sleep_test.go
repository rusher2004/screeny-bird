package upstream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rusher2004/screeny-bird/upstream"
)

func TestTimerSleeperWaitsForTheDuration(t *testing.T) {
	t.Parallel()

	start := time.Now()

	if err := (upstream.TimerSleeper{}).Sleep(context.Background(), 30*time.Millisecond); err != nil {
		t.Fatalf("Sleep() error = %v", err)
	}

	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("Sleep() returned after %v, want at least the requested 30ms", elapsed)
	}
}

func TestTimerSleeperStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()

	if err := (upstream.TimerSleeper{}).Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep() error = %v, want context.Canceled", err)
	}

	if time.Since(start) > 5*time.Second {
		t.Fatal("Sleep() did not return promptly on a canceled context")
	}
}
