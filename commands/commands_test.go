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

type fakeConfigs struct {
	paths   []string
	cfg     config.Config
	newErr  error
	loadErr error
}

func (f *fakeConfigs) New(path string) (commands.ConfigLoader, error) {
	f.paths = append(f.paths, path)

	if f.newErr != nil {
		return nil, f.newErr
	}

	return f, nil
}

func (f *fakeConfigs) Load() (config.Config, error) {
	return f.cfg, f.loadErr
}

type fakeCatalogs struct {
	dirs    []string
	newErr  error
	loadErr error
}

func (f *fakeCatalogs) New(dir string) (commands.CatalogLoader, error) {
	f.dirs = append(f.dirs, dir)

	if f.newErr != nil {
		return nil, f.newErr
	}

	return f, nil
}

// Load returns an empty catalog: the zero value is valid and holds no species.
func (f *fakeCatalogs) Load() (*catalog.Catalog, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}

	return &catalog.Catalog{}, nil
}

func newTOMLLoader(path string) (commands.ConfigLoader, error) {
	loader, err := config.NewTOMLLoader(path)
	if err != nil {
		return nil, err
	}

	return loader, nil
}

func newDirLoader(dir string) (commands.CatalogLoader, error) {
	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		return nil, err
	}

	return loader, nil
}

// run builds the command tree from fakes and runs args. Pass nil fakes for a
// dependency the test does not care about.
func run(t *testing.T, configs *fakeConfigs, catalogs *fakeCatalogs, args ...string) (output string, err error) {
	t.Helper()

	if configs == nil {
		configs = &fakeConfigs{}
	}

	if catalogs == nil {
		catalogs = &fakeCatalogs{}
	}

	return runWith(t, commands.Options{NewConfigLoader: configs.New, NewCatalogLoader: catalogs.New}, args...)
}

// runReal runs args with the real config and catalog loaders.
func runReal(t *testing.T, args ...string) (output string, err error) {
	t.Helper()

	return runWith(t, commands.Options{NewConfigLoader: newTOMLLoader, NewCatalogLoader: newDirLoader}, args...)
}

func runWith(t *testing.T, opts commands.Options, args ...string) (output string, err error) {
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

func TestNewRequiresEveryDependency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	configs, catalogs := &fakeConfigs{}, &fakeCatalogs{}
	full := commands.Options{Output: &buf, NewConfigLoader: configs.New, NewCatalogLoader: catalogs.New}

	tests := map[string]func(commands.Options) commands.Options{
		"output":           func(o commands.Options) commands.Options { o.Output = nil; return o },
		"newConfigLoader":  func(o commands.Options) commands.Options { o.NewConfigLoader = nil; return o },
		"newCatalogLoader": func(o commands.Options) commands.Options { o.NewCatalogLoader = nil; return o },
	}

	for name, drop := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd, err := commands.New(drop(full))
			if err == nil {
				t.Fatalf("New() = %v, want an error without %s", cmd, name)
			}

			if !errors.Is(err, commands.ErrMissingOption) || !strings.Contains(err.Error(), name) {
				t.Fatalf("New() error = %v, want ErrMissingOption naming %s", err, name)
			}
		})
	}

	if _, err := commands.New(full); err != nil {
		t.Fatalf("New() with every dependency error = %v", err)
	}
}

func TestNewReportsEveryMissingDependency(t *testing.T) {
	t.Parallel()

	_, err := commands.New(commands.Options{})
	if !errors.Is(err, commands.ErrMissingOption) {
		t.Fatalf("New() error = %v, want ErrMissingOption", err)
	}

	for _, name := range []string{"output", "newConfigLoader", "newCatalogLoader"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

// Test binaries are built without the version ldflag, so this is the
// unversioned build.
func TestVersionFailsWhenTheBuildSetNone(t *testing.T) {
	t.Parallel()

	configs, catalogs := &fakeConfigs{}, &fakeCatalogs{}

	output, err := run(t, configs, catalogs, "version")
	if !errors.Is(err, commands.ErrNoVersion) {
		t.Fatalf("Run(version) error = %v, want ErrNoVersion", err)
	}

	if output != "" {
		t.Fatalf("Run(version) printed %q, want nothing", output)
	}

	if len(configs.paths) != 0 || len(catalogs.dirs) != 0 {
		t.Fatalf("version built loaders (config %v, catalog %v)", configs.paths, catalogs.dirs)
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

			_, err := run(t, nil, nil, args...)
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

func TestCatalogValidateLoadsConfigThenCatalog(t *testing.T) {
	t.Parallel()

	configs := &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}}
	catalogs := &fakeCatalogs{}

	output, err := run(t, configs, catalogs, "catalog", "validate", "--config", "/etc/screeny.toml")
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

			configs, catalogs := &fakeConfigs{}, &fakeCatalogs{}

			_, err := run(t, configs, catalogs, args...)
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
				t.Errorf("loaders built (config %v, catalog %v) without the flag", configs.paths, catalogs.dirs)
			}
		})
	}
}

func TestCatalogValidateWrapsEachFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")

	tests := []struct {
		name         string
		configs      *fakeConfigs
		catalogs     *fakeCatalogs
		wantCatalogs int
	}{
		{name: "config loader creation", configs: &fakeConfigs{newErr: cause}, catalogs: &fakeCatalogs{}},
		{name: "config load", configs: &fakeConfigs{loadErr: cause}, catalogs: &fakeCatalogs{}},
		{
			name:         "catalog loader creation",
			configs:      &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}},
			catalogs:     &fakeCatalogs{newErr: cause},
			wantCatalogs: 1,
		},
		{
			name:         "catalog load",
			configs:      &fakeConfigs{cfg: config.Config{CatalogDir: "/data/catalog"}},
			catalogs:     &fakeCatalogs{loadErr: cause},
			wantCatalogs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := run(t, tt.configs, tt.catalogs, "catalog", "validate", "--config", "c.toml")
			if !errors.Is(err, cause) {
				t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
			}

			if got := len(tt.catalogs.dirs); got != tt.wantCatalogs {
				t.Fatalf("catalog loaders created = %d, want %d", got, tt.wantCatalogs)
			}
		})
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

	output, err := runReal(t, "catalog", "validate", "--config", valid)
	if err != nil || output != "validated 0 species\n" {
		t.Fatalf("Run() on a valid config and empty catalog = %q, %v; want %q", output, err, "validated 0 species\n")
	}

	_, err = runReal(t, "catalog", "validate", "--config", filepath.Join(configDir, "absent.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() with a missing config file error = %v, want fs.ErrNotExist", err)
	}

	_, err = runReal(t, "catalog", "validate", "--config", emptyCatalog)
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Run() on an empty directory error = %v, want catalog.ErrMissingFile", err)
	}
}
