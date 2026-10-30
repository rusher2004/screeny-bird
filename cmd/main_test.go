package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "version prints and exits zero",
			version:    "1.2.3",
			args:       []string{"screeny", "version"},
			wantCode:   0,
			wantStdout: "1.2.3\n",
		},
		{
			name:       "usage error exits two",
			version:    "1.2.3",
			args:       []string{"screeny", "--no-such-flag"},
			wantCode:   2,
			wantStderr: "screeny: usage: flag provided but not defined: -no-such-flag\n",
		},
		{
			name:       "missing version is a runtime error",
			version:    "",
			args:       []string{"screeny", "version"},
			wantCode:   1,
			wantStderr: "screeny: building command tree: version: missing required option\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := run(context.Background(), tt.version, tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("run() code = %d, want %d", code, tt.wantCode)
			}

			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("run() stdout = %q, want %q", got, tt.wantStdout)
			}

			if got := stderr.String(); got != tt.wantStderr {
				t.Errorf("run() stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}
