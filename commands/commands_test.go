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

func TestCatalogValidateReportsSpeciesCount(t *testing.T) {
	t.Parallel()

	fake := &fakeCatalogs{species: 184}

	stdout, _, err := run(t, commands.Options{Version: "1.2.3", Catalogs: fake}, "catalog", "validate", "--catalog", "/data/catalog")
	if err != nil {
		t.Fatalf("Run(catalog validate) error = %v", err)
	}

	if got, want := stdout, "validated 184 species\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	if len(fake.dirs) != 1 || fake.dirs[0] != "/data/catalog" {
		t.Fatalf("loaded directories = %v, want [/data/catalog]", fake.dirs)
	}
}

func TestCatalogValidateReadsSettingFromInjectedEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "environment supplies the directory",
			env:  map[string]string{"SCREENY_CATALOG": "/from/env"},
			args: []string{"catalog", "validate"},
			want: "/from/env",
		},
		{
			name: "flag wins over environment",
			env:  map[string]string{"SCREENY_CATALOG": "/from/env"},
			args: []string{"catalog", "validate", "--catalog", "/from/flag"},
			want: "/from/flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCatalogs{}

			opts := commands.Options{Version: "1.2.3", Env: tt.env, Catalogs: fake}
			if _, _, err := run(t, opts, tt.args...); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			if len(fake.dirs) != 1 || fake.dirs[0] != tt.want {
				t.Fatalf("loaded directories = %v, want [%s]", fake.dirs, tt.want)
			}
		})
	}
}

func TestCatalogValidateRequiresTheSetting(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  map[string]string
		args []string
	}{
		"nothing provided":      {args: []string{"catalog", "validate"}},
		"empty environment":     {env: map[string]string{"SCREENY_CATALOG": ""}, args: []string{"catalog", "validate"}},
		"empty flag":            {args: []string{"catalog", "validate", "--catalog", ""}},
		"empty flag over env":   {env: map[string]string{"SCREENY_CATALOG": "/from/env"}, args: []string{"catalog", "validate", "--catalog", ""}},
		"unrelated environment": {env: map[string]string{"OTHER": "x"}, args: []string{"catalog", "validate"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCatalogs{}

			_, _, err := run(t, commands.Options{Version: "1.2.3", Env: tt.env, Catalogs: fake}, tt.args...)
			if !errors.Is(err, commands.ErrMissingSetting) {
				t.Fatalf("Run() error = %v, want ErrMissingSetting", err)
			}

			for _, want := range []string{"--catalog", "SCREENY_CATALOG"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %s", err, want)
				}
			}

			if _, isExit := errors.AsType[*commands.ExitError](err); isExit {
				t.Errorf("error %q carries an exit code; a missing setting is a runtime failure", err)
			}

			if len(fake.dirs) != 0 {
				t.Errorf("loader called with %v; it must not run without the setting", fake.dirs)
			}
		})
	}
}

func TestCatalogValidateWrapsLoaderFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("checksum mismatch")
	fake := &fakeCatalogs{err: cause}

	_, _, err := run(t, commands.Options{Version: "1.2.3", Catalogs: fake}, "catalog", "validate", "--catalog", "/data/catalog")
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

func TestCatalogValidateUsesTheRealCatalogPackage(t *testing.T) {
	t.Parallel()

	empty := t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	index := `{"generated_at": null, "schema_version": 1, "species": []}` + "\n"
	if err := os.WriteFile(filepath.Join(empty, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("writing index: %v", err)
	}

	stdout, _, err := run(t, commands.Options{Version: "1.2.3"}, "catalog", "validate", "--catalog", empty)
	if err != nil {
		t.Fatalf("Run() on a valid empty catalog error = %v", err)
	}

	if stdout != "validated 0 species\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "validated 0 species\n")
	}

	_, _, err = run(t, commands.Options{Version: "1.2.3"}, "catalog", "validate", "--catalog", t.TempDir())
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Run() on an empty directory error = %v, want catalog.ErrMissingFile", err)
	}
}
