package server

import "time"

// TimeTicker delivers real clock ticks.
type TimeTicker struct{}

// Tick returns a channel that receives a value every interval until stop is
// called.
func (TimeTicker) Tick(every time.Duration) (ticks <-chan struct{}, stop func()) {
	timer := time.NewTicker(every)
	out := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-timer.C:
				select {
				case out <- struct{}{}:
				default: // a tick is already waiting; coalesce
				}
			case <-done:
				return
			}
		}
	}()

	return out, func() {
		timer.Stop()
		close(done)
	}
}
