package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rusher2004/screeny-bird/commands"
)

func TestRunAndReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantReport string
	}{
		{
			name:       "usage error exits two",
			args:       []string{"screeny", "--no-such-flag"},
			wantCode:   2,
			wantReport: "screeny: usage: flag provided but not defined: -no-such-flag\n",
		},
		{
			// The test binary is built without the version ldflag.
			name:       "an unversioned build is a runtime error",
			args:       []string{"screeny", "version"},
			wantCode:   1,
			wantReport: "screeny: reading the version: version was not set at build time\n",
		},
		{
			name:       "a command that needs the config flag is a runtime error without it",
			args:       []string{"screeny", "catalog", "validate"},
			wantCode:   1,
			wantReport: "screeny: missing required setting: pass --config with the path to the config file\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output, errText bytes.Buffer

			err := run(context.Background(), tt.args, &output)
			if code := report(&errText, err); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if got := errText.String(); got != tt.wantReport {
				t.Errorf("reported error = %q, want %q", got, tt.wantReport)
			}

			if output.Len() != 0 {
				t.Errorf("run() output = %q, want none", output.String())
			}
		})
	}
}

func TestReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantReport string
	}{
		{name: "no error exits zero", err: nil, wantCode: 0},
		{name: "plain error exits one", err: errors.New("boom"), wantCode: 1, wantReport: "screeny: boom\n"},
		{
			name:       "exit error carries its code",
			err:        &commands.ExitError{Code: 7, Msg: "custom"},
			wantCode:   7,
			wantReport: "screeny: custom\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			if code := report(&buf, tt.err); code != tt.wantCode {
				t.Errorf("report() code = %d, want %d", code, tt.wantCode)
			}

			if got := buf.String(); got != tt.wantReport {
				t.Errorf("report() wrote %q, want %q", got, tt.wantReport)
			}
		})
	}
}

func TestRunValidatesACatalogFromAConfigFile(t *testing.T) {
	t.Parallel()

	catalogDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(catalogDir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	index := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"
	if err := os.WriteFile(filepath.Join(catalogDir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "screeny.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("catalog_dir = %q\n", catalogDir)), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	var output bytes.Buffer

	err := run(context.Background(), []string{"screeny", "catalog", "validate", "--config", configPath}, &output)
	if err != nil || output.String() != "validated 0 species\n" {
		t.Fatalf("run() = %q, %v; want %q and no error", output.String(), err, "validated 0 species\n")
	}
}
