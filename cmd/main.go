// Command screeny validates, syncs and serves the bird plate catalog.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/commands"
	"github.com/rusher2004/screeny-bird/config"
)

// configPath is where main expects the settings file. These are the only
// implementations that exist, so main assumes this location; reading it from a
// flag arrives with the first alternative config loader.
const configPath = "/etc/screeny/config.toml"

// configLoader supplies the service's settings from wherever they are kept.
type configLoader interface {
	Load() (config.Config, error)
}

func main() {
	err := run(context.Background(), os.Args, os.Stdout)
	os.Exit(report(os.Stderr, err))
}

// run loads the settings file main assumes and executes the command line,
// writing command output to w.
func run(ctx context.Context, args []string, w io.Writer) error {
	loader, err := config.NewTOMLLoader(configPath)
	if err != nil {
		return fmt.Errorf("creating config loader: %w", err)
	}

	return execute(ctx, loader, args, w)
}

// execute loads settings from loader, builds every dependency from them and
// runs the command line, writing command output to w.
func execute(ctx context.Context, loader configLoader, args []string, w io.Writer) error {
	cfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	catalogs, err := catalog.NewDirLoader(cfg.CatalogDir)
	if err != nil {
		return fmt.Errorf("creating catalog loader: %w", err)
	}

	cmd, err := commands.New(commands.Options{Output: w, Catalogs: catalogs})
	if err != nil {
		return err
	}

	return cmd.Run(ctx, args)
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
