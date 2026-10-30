// Package commands holds the screeny command tree and its composition root.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/config"
)

// ExitUsage is the exit code for a malformed command line.
const ExitUsage = 2

// ErrMissingOption reports that a required Options field was not provided.
var ErrMissingOption = errors.New("missing required option")

// ExitError carries a process exit code that differs from the default of 1.
// It has no Unwrap: its message stands on its own.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	return e.Msg
}

// ErrMissingSetting reports a required command-line setting that was not
// supplied. An empty value counts as not supplied.
var ErrMissingSetting = errors.New("missing required setting")

const configFlag = "config"

// configLoader is what commands need to obtain the service's settings.
type configLoader interface {
	LoadConfig(path string) (config.Config, error)
}

// fileConfigs loads settings from TOML files with the config package.
type fileConfigs struct{}

func (fileConfigs) LoadConfig(path string) (config.Config, error) {
	return config.Load(os.DirFS(filepath.Dir(path)), filepath.Base(path))
}

// catalogLoader is what the validate command needs from the catalog: load the
// catalog in a directory and say how many species it holds.
type catalogLoader interface {
	LoadCatalog(dir string) (species int, err error)
}

// dirCatalogs loads catalogs from directories with the catalog package.
type dirCatalogs struct{}

func (dirCatalogs) LoadCatalog(dir string) (int, error) {
	loaded, err := catalog.Load(os.DirFS(dir))
	if err != nil {
		return 0, err
	}

	return len(loaded.Species()), nil
}

// loadConfig reads the file named by the root --config flag. The path has no
// default and no other source.
func loadConfig(cmd *cli.Command, loader configLoader) (config.Config, error) {
	path := cmd.Root().String(configFlag)
	if strings.TrimSpace(path) == "" {
		return config.Config{}, fmt.Errorf("%w: pass --%s with the path to the config file", ErrMissingSetting, configFlag)
	}

	cfg, err := loader.LoadConfig(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("loading config: %w", err)
	}

	return cfg, nil
}

// Options injects everything the command tree depends on. Version, Stdout and
// Stderr are required: there are no defaults. Configs and Catalogs replace the
// filesystem implementations and exist so tests can substitute them.
type Options struct {
	Version  string
	Stdout   io.Writer
	Stderr   io.Writer
	Configs  configLoader
	Catalogs catalogLoader
}

// New builds the root command. Run on the result returns every error,
// including usage errors, and never prints one or exits the process.
func New(opts Options) (*cli.Command, error) {
	switch {
	case opts.Version == "":
		return nil, fmt.Errorf("building command tree: version: %w", ErrMissingOption)
	case opts.Stdout == nil:
		return nil, fmt.Errorf("building command tree: stdout: %w", ErrMissingOption)
	case opts.Stderr == nil:
		return nil, fmt.Errorf("building command tree: stderr: %w", ErrMissingOption)
	}

	catalogs := opts.Catalogs
	if catalogs == nil {
		catalogs = dirCatalogs{}
	}

	configs := opts.Configs
	if configs == nil {
		configs = fileConfigs{}
	}

	return &cli.Command{
		Name:      "screeny",
		Usage:     "Validate, sync and serve the bird plate catalog",
		Writer:    opts.Stdout,
		ErrWriter: opts.Stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  configFlag,
				Usage: "path to the TOML config file (required by commands that use settings)",
			},
		},
		// The caller owns printing and exit codes, so the library must neither
		// print a returned error nor call os.Exit.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError: func(_ context.Context, _ *cli.Command, err error, _ bool) error {
			return &ExitError{Code: ExitUsage, Msg: fmt.Sprintf("usage: %v", err)}
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Args().Present() {
				return &ExitError{
					Code: ExitUsage,
					Msg:  fmt.Sprintf("usage: unknown command %q", cmd.Args().First()),
				}
			}

			return &ExitError{Code: ExitUsage, Msg: "usage: a command is required"}
		},
		Commands: []*cli.Command{
			{
				Name:  "catalog",
				Usage: "Work with the plate catalog",
				Action: func(context.Context, *cli.Command) error {
					return &ExitError{Code: ExitUsage, Msg: "usage: a catalog command is required"}
				},
				Commands: []*cli.Command{
					{
						Name:  "validate",
						Usage: "Check the catalog against its rules",
						Action: func(_ context.Context, cmd *cli.Command) error {
							cfg, err := loadConfig(cmd, configs)
							if err != nil {
								return err
							}

							species, err := catalogs.LoadCatalog(cfg.CatalogDir)
							if err != nil {
								return fmt.Errorf("validating catalog %q: %w", cfg.CatalogDir, err)
							}

							if _, err := fmt.Fprintf(cmd.Root().Writer, "validated %d species\n", species); err != nil {
								return fmt.Errorf("writing validation result: %w", err)
							}

							return nil
						},
					},
				},
			},
			{
				Name:  "version",
				Usage: "Print the application version",
				Action: func(_ context.Context, cmd *cli.Command) error {
					if _, err := fmt.Fprintln(cmd.Root().Writer, opts.Version); err != nil {
						return fmt.Errorf("writing version: %w", err)
					}

					return nil
				},
			},
		},
	}, nil
}
