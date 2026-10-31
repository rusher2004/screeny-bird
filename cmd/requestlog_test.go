package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRequestLoggerWritesOneStructuredLinePerRequest(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	requestLogger{logger: slog.New(slog.NewTextHandler(&out, nil))}.Log("GET", "/v1/catalog", 200, 1234)

	line := strings.TrimSpace(out.String())
	for _, want := range []string{"msg=request", "method=GET", "path=/v1/catalog", "status=200", "bytes=1234"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q lacks %q", line, want)
		}
	}

	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("logged %q, want exactly one line", out.String())
	}
}
