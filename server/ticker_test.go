package server_test

import (
	"testing"
	"time"

	"github.com/rusher2004/screeny-bird/server"
)

func TestTimeTickerTicksUntilStopped(t *testing.T) {
	t.Parallel()

	ticks, stop := server.TimeTicker{}.Tick(5 * time.Millisecond)

	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatal("no tick arrived")
	}

	stop()

	// Drain a tick that may already be waiting, then expect silence.
	select {
	case <-ticks:
	default:
	}

	select {
	case <-ticks:
		t.Fatal("a tick arrived after stop")
	case <-time.After(100 * time.Millisecond):
	}
}
