package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/rusher2004/screeny-bird/commands"
	"github.com/rusher2004/screeny-bird/config"
	"github.com/rusher2004/screeny-bird/upstream"
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

// validConfig returns a complete config whose catalog is in catalogDir.
func validConfig(catalogDir string) config.Config {
	return config.Config{
		CatalogDir:             catalogDir,
		WorkDir:                filepath.Join(catalogDir, "..", "work"),
		UpstreamRepo:           "veteranbv/inky-bird-frame",
		UpstreamRef:            "main",
		UpstreamTimeoutSeconds: 5,

		UpstreamAttempts:          3,
		UpstreamRetryDelaySeconds: 1,
		ListenAddress:             ":8080",
		ReadHeaderTimeoutSeconds:  5,
		WriteTimeoutSeconds:       5,
		ShutdownTimeoutSeconds:    5,
		ReloadIntervalSeconds:     5,
		HealthcheckTimeoutSeconds: 5,
		CORSAllowedOrigins:        []string{},
	}
}

// blankCatalogDir returns a valid config except that its catalog directory is blank.
func blankCatalogDir(t *testing.T, catalogDir string) config.Config {
	t.Helper()

	cfg := validConfig(catalogDir)
	cfg.CatalogDir = " "

	return cfg
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
	loaded := validConfig(catalogDir)

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
			loader:     &fakeLoader{cfg: blankCatalogDir(t, catalogDir)},
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
			wantReport: "screeny: wiring the HTTP server: creating server: invalid server: version is required\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output, errText bytes.Buffer

			err := execute(context.Background(), tt.version, tt.loader, slog.New(slog.DiscardHandler), tt.args, &output)
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

	content := fmt.Sprintf(`catalog_dir = %q
work_dir = %q
upstream_repo = "a/b"
upstream_ref = "main"
upstream_timeout_seconds = 5
upstream_attempts = 3
upstream_retry_delay_seconds = 1
listen_address = ":8080"
read_header_timeout_seconds = 5
write_timeout_seconds = 5
shutdown_timeout_seconds = 5
reload_interval_seconds = 5
healthcheck_timeout_seconds = 5
cors_allowed_origins = []
`, writeEmptyCatalog(t), t.TempDir())
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	loader, err := config.NewTOMLLoader(path)
	if err != nil {
		t.Fatalf("NewTOMLLoader() error = %v", err)
	}

	var output bytes.Buffer

	if err := execute(context.Background(), "1.2.3", loader, slog.New(slog.DiscardHandler), []string{"screeny", "catalog", "validate"}, &output); err != nil || output.String() != "validated 0 species\n" {
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

func TestNewSyncerBuildsFromAValidConfig(t *testing.T) {
	t.Parallel()

	if _, err := newSyncer(validConfig(t.TempDir())); err != nil {
		t.Fatalf("newSyncer() error = %v", err)
	}
}

func TestNewSyncerRejectsAnInvalidUpstream(t *testing.T) {
	t.Parallel()

	cfg := validConfig(t.TempDir())
	cfg.UpstreamRepo = "not a repo"
	cfg.UpstreamRef = ""

	_, err := newSyncer(cfg)
	if !errors.Is(err, upstream.ErrInvalidSource) {
		t.Fatalf("newSyncer() error = %v, want upstream.ErrInvalidSource", err)
	}
}

func TestNewSyncerRejectsUnusableRetrySettings(t *testing.T) {
	t.Parallel()

	cfg := validConfig(t.TempDir())
	cfg.UpstreamAttempts = 0
	cfg.UpstreamRetryDelaySeconds = 0

	_, err := newSyncer(cfg)
	if !errors.Is(err, upstream.ErrInvalidSource) {
		t.Fatalf("newSyncer() error = %v, want upstream.ErrInvalidSource", err)
	}
}
