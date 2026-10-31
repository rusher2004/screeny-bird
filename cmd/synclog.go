package main

import (
	"log/slog"
	"time"
)

// syncLogger writes the progress of a catalog sync as structured lines. It is
// main's implementation of the progress the syncer declares and the retry
// observer the retrier declares.
type syncLogger struct {
	logger *slog.Logger
}

func (l syncLogger) Resolved(commit string) {
	l.logger.Info("resolved upstream ref", "commit", commit)
}

func (l syncLogger) Planned(upstream, missing int) {
	l.logger.Info("compared with upstream", "upstream", upstream, "missing", missing)
}

func (l syncLogger) Staged(done, total int, species string, reused bool) {
	l.logger.Info("staged species", "done", done, "total", total, "species", species, "reused", reused)
}

func (l syncLogger) Validating() {
	l.logger.Info("validating staged species")
}

func (l syncLogger) Publishing() {
	l.logger.Info("publishing staged species")
}

func (l syncLogger) Retrying(attempt, attempts int, delay time.Duration, err error) {
	l.logger.Warn("upstream request failed, retrying", "attempt", attempt, "attempts", attempts, "delay", delay, "error", err)
}
