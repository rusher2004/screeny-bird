package main

import "log/slog"

// requestLogger writes one structured line per finished request. It is main's
// implementation of the access log the server declares.
type requestLogger struct {
	logger *slog.Logger
}

func (l requestLogger) Log(method, path string, status, bytes int) {
	l.logger.Info("request", "method", method, "path", path, "status", status, "bytes", bytes)
}
