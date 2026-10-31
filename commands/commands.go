// Package commands holds the screeny command tree and its composition root.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/config"
)

// version is injected at build time with
// -ldflags "-X github.com/rusher2004/screeny-bird/commands.version=<version>".
var version string

// ExitUsage is the exit code for a malformed command line.
const ExitUsage = 2

const configFlag = "config"

// ErrMissingOption reports that a required Options field was not provided.
var ErrMissingOption = errors.New("missing required option")

// ErrMissingSetting reports a setting a command needs that was not supplied.
// An empty value counts as not supplied.
var ErrMissingSetting = errors.New("missing required setting")

// ErrNoVersion reports a binary built without a version.
var ErrNoVersion = errors.New("version was not set at build time")

// ExitError carries a process exit code that differs from the default of 1.
// It has no Unwrap: its message stands on its own.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	return e.Msg
}

// configLoader is what commands need to obtain the settings a command uses:
// load the settings kept at path.
type configLoader interface {
	LoadConfig(path string) (config.Config, error)
}

// catalogLoader is what the validate command needs from the catalog: load the
// catalog in a directory.
type catalogLoader interface {
	LoadDir(dir string) (*catalog.Catalog, error)
}

// Options is everything the command tree depends on. Every field is required.
type Options struct {
	Output   io.Writer
	Configs  configLoader
	Catalogs catalogLoader
}

// New builds the command tree from explicit dependencies. Run on the result
// returns every error, including usage errors, and never prints one or exits
// the process.
func New(opts Options) (*cli.Command, error) {
	var missing []error

	if opts.Output == nil {
		missing = append(missing, fmt.Errorf("output: %w", ErrMissingOption))
	}

	if opts.Configs == nil {
		missing = append(missing, fmt.Errorf("configs: %w", ErrMissingOption))
	}

	if opts.Catalogs == nil {
		missing = append(missing, fmt.Errorf("catalogs: %w", ErrMissingOption))
	}

	if err := errors.Join(missing...); err != nil {
		return nil, fmt.Errorf("building command tree: %w", err)
	}

	return &cli.Command{
		Name:   "screeny",
		Usage:  "Validate, sync and serve the bird plate catalog",
		Writer: opts.Output,
		// The library must not print errors itself: main reports each one once.
		ErrWriter: io.Discard,
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
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  configFlag,
								Usage: "path to the TOML config file (required)",
							},
						},
						Action: func(_ context.Context, cmd *cli.Command) error {
							path := cmd.String(configFlag)
							if strings.TrimSpace(path) == "" {
								return fmt.Errorf("%w: pass --%s with the path to the config file", ErrMissingSetting, configFlag)
							}

							cfg, err := opts.Configs.LoadConfig(path)
							if err != nil {
								return fmt.Errorf("loading config: %w", err)
							}

							loaded, err := opts.Catalogs.LoadDir(cfg.CatalogDir)
							if err != nil {
								return fmt.Errorf("validating catalog %q: %w", cfg.CatalogDir, err)
							}

							if _, err := fmt.Fprintf(cmd.Root().Writer, "validated %d species\n", len(loaded.Species())); err != nil {
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
					if version == "" {
						return fmt.Errorf("reading the version: %w", ErrNoVersion)
					}

					if _, err := fmt.Fprintln(cmd.Root().Writer, version); err != nil {
						return fmt.Errorf("writing version: %w", err)
					}

					return nil
				},
			},
		},
	}, nil
}
