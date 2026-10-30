package commands_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/commands"
	"github.com/rusher2004/screeny-bird/config"
)

func run(t *testing.T, opts commands.Options, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	opts.Stdout = &out
	opts.Stderr = &errOut

	cmd, err := commands.New(opts)
	if err != nil {
		return "", "", err
	}

	err = cmd.Run(context.Background(), append([]string{"screeny"}, args...))

	return out.String(), errOut.String(), err
}

func TestNewRejectsMissingOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]commands.Options{
		"version": {Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}},
		"stdout":  {Version: "1.2.3", Stderr: &bytes.Buffer{}},
		"stderr":  {Version: "1.2.3", Stdout: &bytes.Buffer{}},
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

	stdout, _, err := run(t, commands.Options{Version: "1.2.3"}, "version")
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

			_, _, err := run(t, commands.Options{Version: "1.2.3"}, args...)
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

type fakeConfigs struct {
	paths []string
	cfg   config.Config
	err   error
}

func (f *fakeConfigs) LoadConfig(path string) (config.Config, error) {
	f.paths = append(f.paths, path)

	return f.cfg, f.err
}

func TestCatalogValidateReportsSpeciesCount(t *testing.T) {
	t.Parallel()

	configs := &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}}
	catalogs := &fakeCatalogs{species: 184}

	opts := commands.Options{Version: "1.2.3", Configs: configs, Catalogs: catalogs}

	stdout, _, err := run(t, opts, "--config", "/etc/screeny.toml", "catalog", "validate")
	if err != nil {
		t.Fatalf("Run(catalog validate) error = %v", err)
	}

	if got, want := stdout, "validated 184 species\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	if len(configs.paths) != 1 || configs.paths[0] != "/etc/screeny.toml" {
		t.Fatalf("config paths = %v, want [/etc/screeny.toml]", configs.paths)
	}

	if len(catalogs.dirs) != 1 || catalogs.dirs[0] != "/data/catalog" {
		t.Fatalf("catalog directories = %v, want [/data/catalog]", catalogs.dirs)
	}
}

func TestCatalogValidateRequiresTheConfigFlag(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"flag absent": {"catalog", "validate"},
		"flag empty":  {"--config", "", "catalog", "validate"},
		"flag blank":  {"--config", "  ", "catalog", "validate"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			configs := &fakeConfigs{}
			catalogs := &fakeCatalogs{}

			_, _, err := run(t, commands.Options{Version: "1.2.3", Configs: configs, Catalogs: catalogs}, args...)
			if !errors.Is(err, commands.ErrMissingSetting) {
				t.Fatalf("Run() error = %v, want ErrMissingSetting", err)
			}

			if !strings.Contains(err.Error(), "--config") {
				t.Errorf("error %q does not name --config", err)
			}

			if _, isExit := errors.AsType[*commands.ExitError](err); isExit {
				t.Errorf("error %q carries an exit code; a missing setting is a runtime failure", err)
			}

			if len(configs.paths) != 0 || len(catalogs.dirs) != 0 {
				t.Errorf("loaders called (config %v, catalog %v) without the setting", configs.paths, catalogs.dirs)
			}
		})
	}
}

func TestCatalogValidateWrapsConfigFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("unknown settings")
	configs := &fakeConfigs{err: cause}
	catalogs := &fakeCatalogs{}

	_, _, err := run(t, commands.Options{Version: "1.2.3", Configs: configs, Catalogs: catalogs}, "--config", "/etc/screeny.toml", "catalog", "validate")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if len(catalogs.dirs) != 0 {
		t.Fatalf("catalog loaded from %v after the config failed", catalogs.dirs)
	}
}

func TestCatalogValidateWrapsLoaderFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("checksum mismatch")
	configs := &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}}
	catalogs := &fakeCatalogs{err: cause}

	_, _, err := run(t, commands.Options{Version: "1.2.3", Configs: configs, Catalogs: catalogs}, "--config", "c.toml", "catalog", "validate")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if !strings.Contains(err.Error(), "/data/catalog") {
		t.Fatalf("error %q does not name the directory", err)
	}
}

func TestCatalogRequiresASubcommand(t *testing.T) {
	t.Parallel()

	_, _, err := run(t, commands.Options{Version: "1.2.3"}, "catalog")

	exitErr, ok := errors.AsType[*commands.ExitError](err)
	if !ok || exitErr.Code != commands.ExitUsage {
		t.Fatalf("Run(catalog) error = %v, want a usage error with code %d", err, commands.ExitUsage)
	}
}

func TestVersionNeedsNoConfig(t *testing.T) {
	t.Parallel()

	configs := &fakeConfigs{}

	if _, _, err := run(t, commands.Options{Version: "1.2.3", Configs: configs}, "version"); err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}

	if len(configs.paths) != 0 {
		t.Fatalf("version loaded config from %v", configs.paths)
	}
}

func TestCatalogValidateUsesTheRealConfigAndCatalogPackages(t *testing.T) {
	t.Parallel()

	catalogDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(catalogDir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	index := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"
	if err := os.WriteFile(filepath.Join(catalogDir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "screeny.toml")

	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("catalog_dir = %q\n", catalogDir)), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	stdout, _, err := run(t, commands.Options{Version: "1.2.3"}, "--config", configPath, "catalog", "validate")
	if err != nil {
		t.Fatalf("Run() on a valid config and empty catalog error = %v", err)
	}

	if stdout != "validated 0 species\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "validated 0 species\n")
	}

	_, _, err = run(t, commands.Options{Version: "1.2.3"}, "--config", filepath.Join(configDir, "absent.toml"), "catalog", "validate")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() with a missing config file error = %v, want fs.ErrNotExist", err)
	}

	emptyCatalog := filepath.Join(configDir, "other.toml")
	if err := os.WriteFile(emptyCatalog, []byte(fmt.Sprintf("catalog_dir = %q\n", t.TempDir())), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	_, _, err = run(t, commands.Options{Version: "1.2.3"}, "--config", emptyCatalog, "catalog", "validate")
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Run() on an empty directory error = %v, want catalog.ErrMissingFile", err)
	}
}
