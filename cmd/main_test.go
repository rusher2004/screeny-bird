package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
			name:       "missing setting is a runtime error",
			version:    "1.2.3",
			args:       []string{"screeny", "catalog", "validate"},
			wantCode:   1,
			wantStderr: "screeny: missing required setting: pass --config with the path to the config file\n",
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

func TestRunLoadsTheConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	index := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	var stdout, stderr bytes.Buffer

	configPath := filepath.Join(t.TempDir(), "screeny.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("catalog_dir = %q\n", dir)), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	args := []string{"screeny", "--config", configPath, "catalog", "validate"}

	code := run(context.Background(), "1.2.3", args, &stdout, &stderr)
	if code != 0 || stdout.String() != "validated 0 species\n" || stderr.Len() != 0 {
		t.Fatalf("run() = code %d, stdout %q, stderr %q; want 0, %q and no stderr", code, stdout.String(), stderr.String(), "validated 0 species\n")
	}
}
