package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

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

func TestExecute(t *testing.T) {
	t.Parallel()

	catalogDir := writeEmptyCatalog(t)

	tests := []struct {
		name       string
		version    string
		loader     *fakeLoader
		args       []string
		wantCode   int
		wantOutput string
		wantReport string
		wantLoads  int
	}{
		{
			name:       "version prints and exits zero without any config source",
			version:    "1.2.3",
			args:       []string{"screeny", "version"},
			wantOutput: "1.2.3\n",
		},
		{
			name:       "usage error exits two",
			version:    "1.2.3",
			args:       []string{"screeny", "--no-such-flag"},
			wantCode:   2,
			wantReport: "screeny: usage: flag provided but not defined: -no-such-flag\n",
		},
		{
			name:       "missing version is a runtime error",
			args:       []string{"screeny", "version"},
			wantCode:   1,
			wantReport: "screeny: building command tree: version: missing required option\n",
		},
		{
			name:       "a command that needs a setting says how it was left out",
			version:    "1.2.3",
			args:       []string{"screeny", "catalog", "validate"},
			wantCode:   1,
			wantReport: "screeny: catalog_dir: missing required setting (no config source was given: pass --config <path> before the command)\n",
		},
		{
			name:       "loaded settings reach the command",
			version:    "1.2.3",
			loader:     &fakeLoader{cfg: config.Config{CatalogDir: catalogDir}},
			args:       []string{"screeny", "catalog", "validate"},
			wantOutput: "validated 0 species\n",
			wantLoads:  1,
		},
		{
			name:       "config failure stops before the command runs",
			version:    "1.2.3",
			loader:     &fakeLoader{err: errors.New("boom")},
			args:       []string{"screeny", "version"},
			wantCode:   1,
			wantReport: "screeny: loading config: boom\n",
			wantLoads:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				output, errText bytes.Buffer
				loader          configLoader
			)

			// A nil *fakeLoader must stay a nil interface, not a typed nil.
			if tt.loader != nil {
				loader = tt.loader
			}

			err := execute(context.Background(), tt.version, loader, tt.args, &output)
			code := report(&errText, err)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}

			if got := output.String(); got != tt.wantOutput {
				t.Errorf("execute() output = %q, want %q", got, tt.wantOutput)
			}

			if got := errText.String(); got != tt.wantReport {
				t.Errorf("reported error = %q, want %q", got, tt.wantReport)
			}

			if tt.loader != nil && tt.loader.calls != tt.wantLoads {
				t.Errorf("loader called %d times, want %d", tt.loader.calls, tt.wantLoads)
			}
		})
	}
}

func TestSplitConfigFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantPath string
		wantRest []string
		wantErr  bool
	}{
		{name: "no arguments", args: nil},
		{name: "no config flag", args: []string{"catalog", "validate"}, wantRest: []string{"catalog", "validate"}},
		{name: "separate value", args: []string{"--config", "a.toml", "catalog"}, wantPath: "a.toml", wantRest: []string{"catalog"}},
		{name: "joined value", args: []string{"--config=a.toml", "catalog"}, wantPath: "a.toml", wantRest: []string{"catalog"}},
		{name: "only the flag", args: []string{"--config", "a.toml"}, wantPath: "a.toml", wantRest: []string{}},
		{name: "flag after the command is left to the command tree", args: []string{"catalog", "--config", "a.toml"}, wantRest: []string{"catalog", "--config", "a.toml"}},
		{name: "missing value", args: []string{"--config"}, wantErr: true},
		{name: "empty value", args: []string{"--config", ""}, wantErr: true},
		{name: "empty joined value", args: []string{"--config="}, wantErr: true},
		{name: "blank value", args: []string{"--config", "  ", "catalog"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path, rest, err := splitConfigFlag(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitConfigFlag() error = %v, wantErr %t", err, tt.wantErr)
			}

			if path != tt.wantPath || !slices.Equal(rest, tt.wantRest) {
				t.Errorf("splitConfigFlag() = %q, %q; want %q, %q", path, rest, tt.wantPath, tt.wantRest)
			}
		})
	}
}

func TestRunLoadsTheTOMLFileNamedByTheFlag(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "screeny.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("catalog_dir = %q\n", writeEmptyCatalog(t))), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	var output bytes.Buffer

	err := run(context.Background(), "1.2.3", []string{"screeny", "--config", configPath, "catalog", "validate"}, &output)
	if err != nil || output.String() != "validated 0 species\n" {
		t.Fatalf("run() = %q, %v; want %q and no error", output.String(), err, "validated 0 species\n")
	}
}

func TestRunReportsConfigFileProblems(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent.toml")

	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "missing file", args: []string{"screeny", "--config", missing, "version"}, wantCode: 1},
		{name: "flag without a path", args: []string{"screeny", "--config"}, wantCode: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output, errText bytes.Buffer

			err := run(context.Background(), "1.2.3", tt.args, &output)
			if err == nil {
				t.Fatal("run() error = nil, want a failure")
			}

			if code := report(&errText, err); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (reported %q)", code, tt.wantCode, errText.String())
			}
		})
	}
}

func TestReportReturnsZeroForNoError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	if code := report(&buf, nil); code != 0 || buf.Len() != 0 {
		t.Fatalf("report(nil) = code %d, wrote %q; want 0 and nothing", code, buf.String())
	}
}
