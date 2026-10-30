package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rusher2004/screeny-bird/commands"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    string
		cfg        commands.Config
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
			wantStderr: "screeny: missing required setting: pass --catalog or set SCREENY_CATALOG\n",
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

			code := run(context.Background(), tt.version, tt.cfg, tt.args, &stdout, &stderr)

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

func TestConfigFromEnvReadsOnlyDeclaredSettings(t *testing.T) {
	t.Parallel()

	var asked []string

	getenv := func(key string) string {
		asked = append(asked, key)

		return map[string]string{"SCREENY_CATALOG": "/data/catalog", "HOME": "/home/someone"}[key]
	}

	got := configFromEnv(getenv)

	if got.Catalog != "/data/catalog" {
		t.Errorf("configFromEnv().Catalog = %q, want /data/catalog", got.Catalog)
	}

	if len(asked) != 1 || asked[0] != "SCREENY_CATALOG" {
		t.Errorf("configFromEnv() read %v, want only [SCREENY_CATALOG]", asked)
	}
}

func TestRunPassesConfigToCommands(t *testing.T) {
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

	cfg := commands.Config{Catalog: dir}

	code := run(context.Background(), "1.2.3", cfg, []string{"screeny", "catalog", "validate"}, &stdout, &stderr)
	if code != 0 || stdout.String() != "validated 0 species\n" || stderr.Len() != 0 {
		t.Fatalf("run() = code %d, stdout %q, stderr %q; want 0, %q and no stderr", code, stdout.String(), stderr.String(), "validated 0 species\n")
	}
}
