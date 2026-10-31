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

// catalogLoader is the shape of loader commands.Options accepts.
type catalogLoader = interface {
	Load() (*catalog.Catalog, error)
}

type fakeSyncer struct {
	calls  int
	commit string
	added  int
	err    error
}

func (f *fakeSyncer) Sync(context.Context) (string, int, error) {
	f.calls++

	return f.commit, f.added, f.err
}

type fakeServer struct {
	calls int
	err   error
	ctx   context.Context
}

func (f *fakeServer) Serve(ctx context.Context) error {
	f.calls++
	f.ctx = ctx

	return f.err
}

type fakeProber struct {
	calls int
	err   error
}

func (f *fakeProber) Probe(context.Context) error {
	f.calls++

	return f.err
}

func run(t *testing.T, catalogs catalogLoader, args ...string) (output string, err error) {
	t.Helper()

	var buf bytes.Buffer

	return runWith(t, commands.Options{Writer: &buf, Catalogs: catalogs, Syncer: &fakeSyncer{}, Server: &fakeServer{}, Prober: &fakeProber{}, Version: "1.2.3"}, &buf, args...)
}

func runWith(t *testing.T, opts commands.Options, buf *bytes.Buffer, args ...string) (output string, err error) {
	t.Helper()

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

	full := commands.Options{Writer: &buf, Catalogs: &fakeCatalogs{}, Syncer: &fakeSyncer{}, Server: &fakeServer{}, Prober: &fakeProber{}, Version: "1.2.3"}

	tests := map[string]func(commands.Options) commands.Options{
		"writer":   func(o commands.Options) commands.Options { o.Writer = nil; return o },
		"catalogs": func(o commands.Options) commands.Options { o.Catalogs = nil; return o },
		"syncer":   func(o commands.Options) commands.Options { o.Syncer = nil; return o },
		"server":   func(o commands.Options) commands.Options { o.Server = nil; return o },
		"prober":   func(o commands.Options) commands.Options { o.Prober = nil; return o },
		"version":  func(o commands.Options) commands.Options { o.Version = "  "; return o },
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

	for _, name := range []string{"writer", "catalogs", "syncer", "server", "prober", "version"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

func TestVersionPrintsTheInjectedVersion(t *testing.T) {
	t.Parallel()

	catalogs := &fakeCatalogs{}

	output, err := run(t, catalogs, "version")
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}

	if got, want := output, "1.2.3\n"; got != want {
		t.Fatalf("Run(version) output = %q, want %q", got, want)
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

	loaderFor := func(dir string) catalogLoader {
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

func TestCatalogSyncReportsWhatItAdded(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	syncer := &fakeSyncer{commit: "8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e", added: 3}
	opts := commands.Options{Writer: &buf, Catalogs: &fakeCatalogs{}, Syncer: syncer, Server: &fakeServer{}, Prober: &fakeProber{}, Version: "1.2.3"}

	output, err := runWith(t, opts, &buf, "catalog", "sync")
	if err != nil {
		t.Fatalf("Run(catalog sync) error = %v", err)
	}

	if want := "synced 3 new species from 8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e\n"; output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}

	if syncer.calls != 1 {
		t.Fatalf("syncer ran %d times, want 1", syncer.calls)
	}
}

func TestCatalogSyncWrapsAFailureAndPrintsNothing(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	cause := errors.New("upstream changed")
	opts := commands.Options{Writer: &buf, Catalogs: &fakeCatalogs{}, Syncer: &fakeSyncer{err: cause}, Server: &fakeServer{}, Prober: &fakeProber{}, Version: "1.2.3"}

	output, err := runWith(t, opts, &buf, "catalog", "sync")
	if !errors.Is(err, cause) {
		t.Fatalf("Run() error = %v, want it to wrap %v", err, cause)
	}

	if output != "" {
		t.Fatalf("Run() printed %q after a failure, want nothing", output)
	}
}

func TestServeRunsTheServerUntilItReturns(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	srv := &fakeServer{}
	opts := commands.Options{Writer: &buf, Catalogs: &fakeCatalogs{}, Syncer: &fakeSyncer{}, Server: srv, Prober: &fakeProber{}, Version: "1.2.3"}

	if _, err := runWith(t, opts, &buf, "serve"); err != nil {
		t.Fatalf("Run(serve) error = %v", err)
	}

	if srv.calls != 1 || srv.ctx == nil {
		t.Fatalf("server ran %d times with context %v, want once with the command's context", srv.calls, srv.ctx)
	}
}

func TestServeWrapsAServerFailure(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	cause := errors.New("address in use")
	opts := commands.Options{Writer: &buf, Catalogs: &fakeCatalogs{}, Syncer: &fakeSyncer{}, Server: &fakeServer{err: cause}, Prober: &fakeProber{}, Version: "1.2.3"}

	if _, err := runWith(t, opts, &buf, "serve"); !errors.Is(err, cause) {
		t.Fatalf("Run(serve) error = %v, want it to wrap %v", err, cause)
	}
}

func TestHealthcheckPrintsOkOnlyWhenHealthy(t *testing.T) {
	t.Parallel()

	var healthy bytes.Buffer

	opts := commands.Options{Writer: &healthy, Catalogs: &fakeCatalogs{}, Syncer: &fakeSyncer{}, Server: &fakeServer{}, Prober: &fakeProber{}, Version: "1.2.3"}

	output, err := runWith(t, opts, &healthy, "healthcheck")
	if err != nil || output != "ok\n" {
		t.Fatalf("Run(healthcheck) = %q, %v; want ok", output, err)
	}

	var sick bytes.Buffer

	cause := errors.New("answered 503")
	opts = commands.Options{Writer: &sick, Catalogs: &fakeCatalogs{}, Syncer: &fakeSyncer{}, Server: &fakeServer{}, Prober: &fakeProber{err: cause}, Version: "1.2.3"}

	output, err = runWith(t, opts, &sick, "healthcheck")
	if !errors.Is(err, cause) || output != "" {
		t.Fatalf("Run(healthcheck) = %q, %v; want no output and an error wrapping %v", output, err, cause)
	}
}
