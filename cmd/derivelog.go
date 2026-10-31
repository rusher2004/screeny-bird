package main

import "log/slog"

// deriveLogger writes the progress of a catalog derive as structured lines. It
// is main's implementation of the progress the deriver declares.
type deriveLogger struct {
	logger *slog.Logger
}

func (l deriveLogger) Planned(total, missing int) {
	l.logger.Info("compared with the catalog", "species", total, "missing", missing)
}

func (l deriveLogger) Derived(done, missing int, species string) {
	l.logger.Info("derived image", "done", done, "total", missing, "species", species)
}
