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

type fakeCatalogs struct {
	dirs []string
	err  error
}

// LoadDir returns an empty catalog: the zero value is valid and holds no species.
func (f *fakeCatalogs) LoadDir(dir string) (*catalog.Catalog, error) {
	f.dirs = append(f.dirs, dir)

	if f.err != nil {
		return nil, f.err
	}

	return &catalog.Catalog{}, nil
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

func TestNewRejectsMissingOutput(t *testing.T) {
	t.Parallel()

	cmd, err := commands.New(commands.Options{Version: "1.2.3"})
	if err == nil {
		t.Fatalf("New() = %v, want an error without an Output", cmd)
	}

	if !errors.Is(err, commands.ErrMissingOption) || !strings.Contains(err.Error(), "output") {
		t.Fatalf("New() error = %v, want ErrMissingOption naming output", err)
	}
}

func TestVersionPrintsTheVersion(t *testing.T) {
	t.Parallel()

	output, err := run(t, commands.Options{Version: "1.2.3"}, "version")
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}

	if got, want := output, "1.2.3\n"; got != want {
		t.Fatalf("Run(version) output = %q, want %q", got, want)
	}
}

// Test binaries are built without the version ldflag, so this is the
// unversioned build.
func TestVersionFailsWhenTheBuildSetNone(t *testing.T) {
	t.Parallel()

	output, err := run(t, commands.Options{}, "version")
	if !errors.Is(err, commands.ErrNoVersion) {
		t.Fatalf("Run(version) error = %v, want ErrNoVersion", err)
	}

	if output != "" {
		t.Fatalf("Run(version) printed %q, want nothing", output)
	}
}

func TestVersionNeedsNoConfig(t *testing.T) {
	t.Parallel()

	configs := &fakeConfigs{}

	if _, err := run(t, commands.Options{Version: "1.2.3", Configs: configs}, "version"); err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}

	if len(configs.paths) != 0 {
		t.Fatalf("version loaded config from %v", configs.paths)
	}
}

func TestUsageErrorsCarryExitCodeTwo(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"unknown flag":    {"--no-such-flag"},
		"unknown command": {"no-such-command"},
		"no command":      {},
		"catalog alone":   {"catalog"},
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

func TestCatalogValidateReportsSpeciesCount(t *testing.T) {
	t.Parallel()

	configs := &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}}
	catalogs := &fakeCatalogs{}

	output, err := run(t, commands.Options{Configs: configs, Catalogs: catalogs}, "catalog", "validate", "--config", "/etc/screeny.toml")
	if err != nil {
		t.Fatalf("Run(catalog validate) error = %v", err)
	}

	if got, want := output, "validated 0 species\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
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
		"flag empty":  {"catalog", "validate", "--config", ""},
		"flag blank":  {"catalog", "validate", "--config", "  "},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			configs := &fakeConfigs{}
			catalogs := &fakeCatalogs{}

			_, err := run(t, commands.Options{Configs: configs, Catalogs: catalogs}, args...)
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
				t.Errorf("loaders called (config %v, catalog %v) without the flag", configs.paths, catalogs.dirs)
			}
		})
	}
}

func TestCatalogValidateWrapsConfigFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("unknown settings")
	configs := &fakeConfigs{err: cause}
	catalogs := &fakeCatalogs{}

	_, err := run(t, commands.Options{Configs: configs, Catalogs: catalogs}, "catalog", "validate", "--config", "c.toml")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if len(catalogs.dirs) != 0 {
		t.Fatalf("catalog loaded from %v after the config failed", catalogs.dirs)
	}
}

func TestCatalogValidateWrapsCatalogFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("checksum mismatch")
	configs := &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}}
	catalogs := &fakeCatalogs{err: cause}

	_, err := run(t, commands.Options{Configs: configs, Catalogs: catalogs}, "catalog", "validate", "--config", "c.toml")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if !strings.Contains(err.Error(), "/data/catalog") {
		t.Fatalf("error %q does not name the directory", err)
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
	write := func(name, content string) string {
		path := filepath.Join(configDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}

		return path
	}

	valid := write("valid.toml", fmt.Sprintf("catalog_dir = %q\n", catalogDir))
	emptyCatalog := write("empty.toml", fmt.Sprintf("catalog_dir = %q\n", t.TempDir()))

	output, err := run(t, commands.Options{}, "catalog", "validate", "--config", valid)
	if err != nil || output != "validated 0 species\n" {
		t.Fatalf("Run() on a valid config and empty catalog = %q, %v; want %q", output, err, "validated 0 species\n")
	}

	_, err = run(t, commands.Options{}, "catalog", "validate", "--config", filepath.Join(configDir, "absent.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() with a missing config file error = %v, want fs.ErrNotExist", err)
	}

	_, err = run(t, commands.Options{}, "catalog", "validate", "--config", emptyCatalog)
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Run() on an empty directory error = %v, want catalog.ErrMissingFile", err)
	}
}
