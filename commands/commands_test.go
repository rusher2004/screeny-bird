package commands_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/commands"
)

func run(t *testing.T, opts commands.Options, args ...string) (output string, err error) {
	t.Helper()

	var buf bytes.Buffer
	opts.Output = &buf

	cmd, err := commands.New(opts)
	if err != nil {
		return "", err
	}

	err = cmd.Run(context.Background(), append([]string{"screeny"}, args...))

	return buf.String(), err
}

func TestNewRejectsMissingOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]commands.Options{
		"version": {Output: &bytes.Buffer{}},
		"output":  {Version: "1.2.3"},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd, err := commands.New(opts)
			if err == nil {
				t.Fatalf("New() = %v, want error for missing %s", cmd, name)
			}

			if !errors.Is(err, commands.ErrMissingOption) {
				t.Fatalf("New() error = %v, want ErrMissingOption", err)
			}

			if !strings.Contains(err.Error(), name) {
				t.Fatalf("New() error = %q, want it to name %q", err, name)
			}
		})
	}
}

func TestVersionPrintsVersion(t *testing.T) {
	t.Parallel()

	stdout, err := run(t, commands.Options{Version: "1.2.3"}, "version")
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}

	if got, want := stdout, "1.2.3\n"; got != want {
		t.Fatalf("Run(version) stdout = %q, want %q", got, want)
	}
}

func TestUsageErrorsCarryExitCodeTwo(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"unknown flag":    {"--no-such-flag"},
		"unknown command": {"no-such-command"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := run(t, commands.Options{Version: "1.2.3"}, args...)
			if err == nil {
				t.Fatal("Run() error = nil, want usage error")
			}

			exitErr, ok := errors.AsType[*commands.ExitError](err)
			if !ok {
				t.Fatalf("Run() error = %v, want *commands.ExitError in chain", err)
			}

			if exitErr.Code != commands.ExitUsage {
				t.Fatalf("exit code = %d, want %d", exitErr.Code, commands.ExitUsage)
			}
		})
	}
}

type fakeCatalogs struct {
	dirs    []string
	species int
	err     error
}

func (f *fakeCatalogs) LoadCatalog(dir string) (int, error) {
	f.dirs = append(f.dirs, dir)

	return f.species, f.err
}

func TestCatalogValidateReportsSpeciesCount(t *testing.T) {
	t.Parallel()

	catalogs := &fakeCatalogs{species: 184}
	opts := commands.Options{Version: "1.2.3", Settings: commands.Settings{CatalogDir: "/data/catalog"}, Catalogs: catalogs}

	stdout, err := run(t, opts, "catalog", "validate")
	if err != nil {
		t.Fatalf("Run(catalog validate) error = %v", err)
	}

	if got, want := stdout, "validated 184 species\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	if len(catalogs.dirs) != 1 || catalogs.dirs[0] != "/data/catalog" {
		t.Fatalf("catalog directories = %v, want [/data/catalog]", catalogs.dirs)
	}
}

func TestCatalogValidateRequiresTheCatalogDirSetting(t *testing.T) {
	t.Parallel()

	tests := map[string]commands.Settings{
		"not supplied": {},
		"blank":        {CatalogDir: "  "},
	}

	for name, settings := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalogs := &fakeCatalogs{}

			_, err := run(t, commands.Options{Version: "1.2.3", Settings: settings, Catalogs: catalogs}, "catalog", "validate")
			if !errors.Is(err, commands.ErrMissingSetting) {
				t.Fatalf("Run() error = %v, want ErrMissingSetting", err)
			}

			if !strings.Contains(err.Error(), "catalog_dir") {
				t.Errorf("error %q does not name catalog_dir", err)
			}

			if _, isExit := errors.AsType[*commands.ExitError](err); isExit {
				t.Errorf("error %q carries an exit code; a missing setting is a runtime failure", err)
			}

			if len(catalogs.dirs) != 0 {
				t.Errorf("catalog loaded from %v without the setting", catalogs.dirs)
			}
		})
	}
}

func TestCatalogValidateWrapsLoaderFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("checksum mismatch")
	catalogs := &fakeCatalogs{err: cause}
	opts := commands.Options{Version: "1.2.3", Settings: commands.Settings{CatalogDir: "/data/catalog"}, Catalogs: catalogs}

	_, err := run(t, opts, "catalog", "validate")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if !strings.Contains(err.Error(), "/data/catalog") {
		t.Fatalf("error %q does not name the directory", err)
	}
}

func TestCatalogRequiresASubcommand(t *testing.T) {
	t.Parallel()

	_, err := run(t, commands.Options{Version: "1.2.3"}, "catalog")

	exitErr, ok := errors.AsType[*commands.ExitError](err)
	if !ok || exitErr.Code != commands.ExitUsage {
		t.Fatalf("Run(catalog) error = %v, want a usage error with code %d", err, commands.ExitUsage)
	}
}

func TestVersionNeedsNoSettings(t *testing.T) {
	t.Parallel()

	stdout, err := run(t, commands.Options{Version: "1.2.3"}, "version")
	if err != nil || stdout != "1.2.3\n" {
		t.Fatalf("Run(version) = %q, %v; want 1.2.3 with no settings", stdout, err)
	}
}

func TestCatalogValidateUsesTheRealCatalogPackage(t *testing.T) {
	t.Parallel()

	valid := t.TempDir()
	if err := os.Mkdir(filepath.Join(valid, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	index := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"
	if err := os.WriteFile(filepath.Join(valid, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	stdout, err := run(t, commands.Options{Version: "1.2.3", Settings: commands.Settings{CatalogDir: valid}}, "catalog", "validate")
	if err != nil {
		t.Fatalf("Run() on a valid empty catalog error = %v", err)
	}

	if stdout != "validated 0 species\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "validated 0 species\n")
	}

	_, err = run(t, commands.Options{Version: "1.2.3", Settings: commands.Settings{CatalogDir: t.TempDir()}}, "catalog", "validate")
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Run() on an empty directory error = %v, want catalog.ErrMissingFile", err)
	}
}
