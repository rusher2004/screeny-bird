package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestDeriveLoggerWritesOneLinePerEvent(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	l := deriveLogger{logger: slog.New(slog.NewTextHandler(&out, nil))}

	l.Planned(184, 12)
	l.Derived(3, 12, "1001-carolina-wren")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	wants := [][]string{
		{"species=184", "missing=12"},
		{"done=3", "total=12", "species=1001-carolina-wren"},
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
