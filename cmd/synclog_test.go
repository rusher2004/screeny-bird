package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestSyncLoggerWritesOneLinePerEvent(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	l := syncLogger{logger: slog.New(slog.NewTextHandler(&out, nil))}

	l.Resolved("8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e")
	l.Planned(184, 120)
	l.Staged(3, 120, "1001-carolina-wren", true)
	l.Validating()
	l.Publishing()
	l.Retrying(1, 3, 5*time.Second, errors.New("stalled"))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	wants := [][]string{
		{"commit=8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e"},
		{"upstream=184", "missing=120"},
		{"done=3", "total=120", "species=1001-carolina-wren", "reused=true"},
		{"validating"},
		{"publishing"},
		{"level=WARN", "attempt=1", "attempts=3", "delay=5s", "error=stalled"},
	}

	if len(lines) != len(wants) {
		t.Fatalf("logged %d lines, want %d:\n%s", len(lines), len(wants), out.String())
	}

	for i, want := range wants {
		for _, part := range want {
			if !strings.Contains(lines[i], part) {
				t.Errorf("line %d %q lacks %q", i, lines[i], part)
			}
		}
	}
}
