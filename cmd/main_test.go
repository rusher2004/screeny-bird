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
	"github.com/rusher2004/screeny-bird/config"
)

type fakeLoader struct {
	cfg   config.Config
	err   error
	calls int
}

func (f *fakeLoader) Load() (config.Config, error) {
	f.calls++

	return f.cfg, f.err
}

// writeEmptyCatalog creates a valid catalog with no species and returns its directory.
func writeEmptyCatalog(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	index := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	return dir
}

func TestExecuteAndReport(t *testing.T) {
	t.Parallel()

	catalogDir := writeEmptyCatalog(t)
	loaded := config.Config{CatalogDir: catalogDir}

	tests := []struct {
		name       string
		version    string
		loader     *fakeLoader
		args       []string
		wantCode   int
		wantOutput string
		wantReport string
	}{
		{
			name:       "settings reach the catalog command",
			version:    "1.2.3",
			loader:     &fakeLoader{cfg: loaded},
			args:       []string{"screeny", "catalog", "validate"},
			wantOutput: "validated 0 species\n",
		},
		{
			name:       "a config failure stops before the command runs",
			version:    "1.2.3",
			loader:     &fakeLoader{err: errors.New("boom")},
			args:       []string{"screeny", "catalog", "validate"},
			wantCode:   1,
			wantReport: "screeny: loading config: boom\n",
		},
		{
			name:       "a blank catalog directory fails while building the loader",
			version:    "1.2.3",
			loader:     &fakeLoader{cfg: config.Config{CatalogDir: " "}},
			args:       []string{"screeny", "catalog", "validate"},
			wantCode:   1,
			wantReport: "screeny: creating catalog loader: creating directory loader: catalog directory is required\n",
		},
		{
			name:       "usage error exits two",
			version:    "1.2.3",
			loader:     &fakeLoader{cfg: loaded},
			args:       []string{"screeny", "--no-such-flag"},
			wantCode:   2,
			wantReport: "screeny: usage: flag provided but not defined: -no-such-flag\n",
		},
		{
			name:       "the version command prints the injected version",
			version:    "1.2.3",
			loader:     &fakeLoader{cfg: loaded},
			args:       []string{"screeny", "version"},
			wantOutput: "1.2.3\n",
		},
		{
			name:       "a build without a version fails fast",
			loader:     &fakeLoader{cfg: loaded},
			args:       []string{"screeny", "version"},
			wantCode:   1,
			wantReport: "screeny: building command tree: version: missing required option\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output, errText bytes.Buffer

			err := execute(context.Background(), tt.version, tt.loader, tt.args, &output)
			if code := report(&errText, err); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if got := output.String(); got != tt.wantOutput {
				t.Errorf("execute() output = %q, want %q", got, tt.wantOutput)
			}

			if got := errText.String(); got != tt.wantReport {
				t.Errorf("reported error = %q, want %q", got, tt.wantReport)
			}

			if tt.loader.calls != 1 {
				t.Errorf("config loaded %d times, want 1", tt.loader.calls)
			}
		})
	}
}

func TestExecuteLoadsTheRealTOMLFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "screeny.toml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("catalog_dir = %q\n", writeEmptyCatalog(t))), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	loader, err := config.NewTOMLLoader(path)
	if err != nil {
		t.Fatalf("NewTOMLLoader() error = %v", err)
	}

	var output bytes.Buffer

	if err := execute(context.Background(), "1.2.3", loader, []string{"screeny", "catalog", "validate"}, &output); err != nil || output.String() != "validated 0 species\n" {
		t.Fatalf("execute() = %q, %v; want %q and no error", output.String(), err, "validated 0 species\n")
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
