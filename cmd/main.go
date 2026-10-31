// Command screeny validates, syncs and serves the bird plate catalog.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/commands"
	"github.com/rusher2004/screeny-bird/config"
	"github.com/rusher2004/screeny-bird/derive"
	"github.com/rusher2004/screeny-bird/mirror"
	"github.com/rusher2004/screeny-bird/upstream"
)

// version is injected at build time with -ldflags "-X main.version=<version>".
var version string

// configPath is where main expects the settings file. These are the only
// implementations that exist, so main assumes this location; reading it from a
// flag arrives with the first alternative config loader.
const configPath = "/etc/screeny/config.toml"

// configLoader supplies the service's settings from wherever they are kept.
type configLoader interface {
	Load() (config.Config, error)
}

func main() {
	// main is the only code that touches signals: SIGINT and SIGTERM end the
	// context the serve command runs under, which shuts the server down.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(ctx, version, logger, os.Args, os.Stdout)

	stop()
	os.Exit(report(os.Stderr, err))
}

// run loads the settings file main assumes and executes the command line,
// writing command output to w.
func run(ctx context.Context, version string, logger *slog.Logger, args []string, w io.Writer) error {
	loader, err := config.NewTOMLLoader(configPath)
	if err != nil {
		return fmt.Errorf("creating config loader: %w", err)
	}

	return execute(ctx, version, loader, logger, args, w)
}

// execute loads settings from loader, builds every dependency from them and
// runs the command line, writing command output to w.
func execute(ctx context.Context, version string, loader configLoader, logger *slog.Logger, args []string, w io.Writer) error {
	cfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	catalogs, err := catalog.NewDirLoader(cfg.CatalogDir)
	if err != nil {
		return fmt.Errorf("creating catalog loader: %w", err)
	}

	syncer, err := newSyncer(cfg, logger)
	if err != nil {
		return err
	}

	deriver, err := newDeriver(cfg, catalogs, logger)
	if err != nil {
		return err
	}

	derived, err := derive.NewDirLoader(cfg.DerivedDir)
	if err != nil {
		return fmt.Errorf("wiring derived images: %w", err)
	}

	srv, err := newServer(cfg, version, catalogs, derived, logger)
	if err != nil {
		return err
	}

	prober, err := newProber(cfg)
	if err != nil {
		return err
	}

	cmd, err := commands.New(commands.Options{
		Writer:   w,
		Catalogs: catalogs,
		Syncer:   syncer,
		Deriver:  deriver,
		Server:   srv,
		Prober:   prober,
		Version:  version,
	})
	if err != nil {
		return err
	}

	return cmd.Run(ctx, args)
}

// newSyncer builds the upstream source and the syncer that mirrors it into the
// configured store. The HTTP client has an explicit configuration: no proxy
// lookup, no redirects and the configured timeout, so nothing outside the
// settings decides where a request goes.
func newSyncer(cfg config.Config, logger *slog.Logger) (mirror.Syncer, error) {
	timeout := time.Duration(cfg.UpstreamTimeoutSeconds) * time.Second

	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil, TLSHandshakeTimeout: timeout},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	source, err := upstream.NewSource(cfg.UpstreamRepo, cfg.UpstreamRef, client)
	if err != nil {
		return mirror.Syncer{}, fmt.Errorf("creating upstream source: %w", err)
	}

	retrier, err := upstream.NewRetrier(
		source,
		cfg.UpstreamAttempts,
		time.Duration(cfg.UpstreamRetryDelaySeconds)*time.Second,
		upstream.TimerSleeper{},
		syncLogger{logger: logger},
	)
	if err != nil {
		return mirror.Syncer{}, fmt.Errorf("creating upstream retrier: %w", err)
	}

	syncer, err := mirror.NewSyncer(retrier, syncLogger{logger: logger}, cfg.UpstreamRepo, cfg.UpstreamRef, cfg.CatalogDir, cfg.WorkDir)
	if err != nil {
		return mirror.Syncer{}, fmt.Errorf("creating syncer: %w", err)
	}

	return syncer, nil
}

// newDeriver builds the deriver that writes the configured size of image from
// the catalog loaded by catalogs.
func newDeriver(cfg config.Config, catalogs catalog.DirLoader, logger *slog.Logger) (derive.Deriver, error) {
	deriver, err := derive.NewDeriver(catalogs, deriveLogger{logger: logger}, cfg.DerivedDir, cfg.DeriveWidth, cfg.DeriveHeight)
	if err != nil {
		return derive.Deriver{}, fmt.Errorf("creating deriver: %w", err)
	}

	return deriver, nil
}

// report prints err to w once and returns the process exit code: 0 for no
// error, the code an *commands.ExitError carries, or 1.
func report(w io.Writer, err error) int {
	if err == nil {
		return 0
	}

	// Nothing useful can be done if the writer itself fails, and the exit code
	// below already reports the failure.
	_, _ = fmt.Fprintf(w, "screeny: %v\n", err)

	if exitErr, ok := errors.AsType[*commands.ExitError](err); ok {
		return exitErr.Code
	}

	return 1
}
