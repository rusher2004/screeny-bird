package upstream

import (
	"context"
	"fmt"
	"time"
)

// TimerSleeper waits on the real clock.
type TimerSleeper struct{}

// Sleep waits for d, or until ctx ends, whichever comes first. It returns the
// context's error if it ended first.
func (TimerSleeper) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sleeping %v: %w", d, ctx.Err())
	}
}
