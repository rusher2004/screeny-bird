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

type fakeCatalogs struct {
	calls   int
	loadErr error
}

// Load returns an empty catalog: the zero value is valid and holds no species.
func (f *fakeCatalogs) Load() (*catalog.Catalog, error) {
	f.calls++

	if f.loadErr != nil {
		return nil, f.loadErr
	}

	return &catalog.Catalog{}, nil
}

func run(t *testing.T, catalogs commands.CatalogLoader, args ...string) (output string, err error) {
	t.Helper()

	var buf bytes.Buffer

	cmd, err := commands.New(commands.Options{Output: &buf, Catalogs: catalogs})
	if err != nil {
		return "", err
	}

	err = cmd.Run(context.Background(), append([]string{"screeny"}, args...))

	return buf.String(), err
}

func TestNewRequiresEveryDependency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	full := commands.Options{Output: &buf, Catalogs: &fakeCatalogs{}}

	tests := map[string]func(commands.Options) commands.Options{
		"output":   func(o commands.Options) commands.Options { o.Output = nil; return o },
		"catalogs": func(o commands.Options) commands.Options { o.Catalogs = nil; return o },
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

	for _, name := range []string{"output", "catalogs"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

// Test binaries are built without the version ldflag, so this is the
// unversioned build.
func TestVersionFailsWhenTheBuildSetNone(t *testing.T) {
	t.Parallel()

	catalogs := &fakeCatalogs{}

	output, err := run(t, catalogs, "version")
	if !errors.Is(err, commands.ErrNoVersion) {
		t.Fatalf("Run(version) error = %v, want ErrNoVersion", err)
	}

	if output != "" {
		t.Fatalf("Run(version) printed %q, want nothing", output)
	}

	if catalogs.calls != 0 {
		t.Fatalf("version loaded the catalog %d times", catalogs.calls)
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

			_, err := run(t, &fakeCatalogs{}, args...)
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

func TestCatalogValidateReportsTheSpeciesCount(t *testing.T) {
	t.Parallel()

	catalogs := &fakeCatalogs{}

	output, err := run(t, catalogs, "catalog", "validate")
	if err != nil {
		t.Fatalf("Run(catalog validate) error = %v", err)
	}

	if got, want := output, "validated 0 species\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	if catalogs.calls != 1 {
		t.Fatalf("catalog loaded %d times, want 1", catalogs.calls)
	}
}

func TestCatalogValidateWrapsALoadFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("checksum mismatch")

	output, err := run(t, &fakeCatalogs{loadErr: cause}, "catalog", "validate")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if output != "" {
		t.Fatalf("Run() printed %q after a failure, want nothing", output)
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

	loaderFor := func(dir string) commands.CatalogLoader {
		loader, err := catalog.NewDirLoader(dir)
		if err != nil {
			t.Fatalf("NewDirLoader(%q) error = %v", dir, err)
		}

		return loader
	}

	output, err := run(t, loaderFor(valid), "catalog", "validate")
	if err != nil || output != "validated 0 species\n" {
		t.Fatalf("Run() on an empty valid catalog = %q, %v; want %q", output, err, "validated 0 species\n")
	}

	_, err = run(t, loaderFor(t.TempDir()), "catalog", "validate")
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Run() on an empty directory error = %v, want catalog.ErrMissingFile", err)
	}
}
