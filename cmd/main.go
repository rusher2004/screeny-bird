// Command screeny validates, syncs and serves the bird plate catalog.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rusher2004/screeny-bird/commands"
	"github.com/rusher2004/screeny-bird/config"
)

// version is injected at build time with -ldflags "-X main.version=<version>".
var version string

// configLoader supplies the service's settings from wherever they are kept: a
// TOML file today, and any other source that can produce a config.Config.
type configLoader interface {
	Load() (config.Config, error)
}

func main() {
	err := run(context.Background(), version, os.Args, os.Stdout)
	os.Exit(report(os.Stderr, err))
}

// run reads main's own leading --config flag, wires the TOML file loader it
// names, and executes the rest of the command line, writing command output to w.
func run(ctx context.Context, version string, args []string, w io.Writer) error {
	if len(args) == 0 {
		return &commands.ExitError{Code: commands.ExitUsage, Msg: "usage: missing program name"}
	}

	path, rest, err := splitConfigFlag(args[1:])
	if err != nil {
		return err
	}

	var loader configLoader
	if path != "" {
		loader = config.TOMLFile{FS: os.DirFS(filepath.Dir(path)), Name: filepath.Base(path)}
	}

	return execute(ctx, version, loader, append([]string{args[0]}, rest...), w)
}

// splitConfigFlag removes a leading --config <path> or --config=<path> from
// args. Only the leading position is main's: anything later belongs to the
// command tree.
func splitConfigFlag(args []string) (path string, rest []string, err error) {
	if len(args) == 0 {
		return "", args, nil
	}

	switch arg := args[0]; {
	case arg == "--config":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return "", nil, &commands.ExitError{Code: commands.ExitUsage, Msg: "usage: --config needs the path to a config file"}
		}

		return args[1], args[2:], nil
	case strings.HasPrefix(arg, "--config="):
		value := strings.TrimPrefix(arg, "--config=")
		if strings.TrimSpace(value) == "" {
			return "", nil, &commands.ExitError{Code: commands.ExitUsage, Msg: "usage: --config needs the path to a config file"}
		}

		return value, args[1:], nil
	}

	return "", args, nil
}

// execute loads settings from loader, when there is one, and runs the command
// line, writing command output to w.
func execute(ctx context.Context, version string, loader configLoader, args []string, w io.Writer) error {
	var settings commands.Settings

	if loader != nil {
		cfg, err := loader.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		settings = commands.Settings{CatalogDir: cfg.CatalogDir}
	}

	cmd, err := commands.New(commands.Options{Version: version, Output: w, Settings: settings})
	if err != nil {
		return err
	}

	if err := cmd.Run(ctx, args); err != nil {
		if loader == nil && errors.Is(err, commands.ErrMissingSetting) {
			return fmt.Errorf("%w (no config source was given: pass --config <path> before the command)", err)
		}

		return err
	}

	return nil
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
