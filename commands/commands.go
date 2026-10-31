// Package commands holds the screeny command tree.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rusher2004/screeny-bird/catalog"
)

// version is injected at build time with
// -ldflags "-X github.com/rusher2004/screeny-bird/commands.version=<version>".
var version string

// ExitUsage is the exit code for a malformed command line.
const ExitUsage = 2

// ErrMissingOption reports that a required Options field was not provided.
var ErrMissingOption = errors.New("missing required option")

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

// catalogLoader loads the catalog a command validates.
type catalogLoader interface {
	Load() (*catalog.Catalog, error)
}

// Options is everything the command tree depends on. Every field is required.
type Options struct {
	Writer   io.Writer
	Catalogs catalogLoader
}

// New builds the command tree from explicit dependencies. Run on the result
// returns every error, including usage errors, and never prints one or exits
// the process.
func New(opts Options) (*cli.Command, error) {
	var missing []error

	if opts.Writer == nil {
		missing = append(missing, fmt.Errorf("writer: %w", ErrMissingOption))
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
		Writer: opts.Writer,
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
						Action: func(context.Context, *cli.Command) error {
							loaded, err := opts.Catalogs.Load()
							if err != nil {
								return fmt.Errorf("validating catalog: %w", err)
							}

							if _, err := fmt.Fprintf(opts.Writer, "validated %d species\n", len(loaded.Species())); err != nil {
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
				Action: func(context.Context, *cli.Command) error {
					if version == "" {
						return fmt.Errorf("reading the version: %w", ErrNoVersion)
					}

					if _, err := fmt.Fprintln(opts.Writer, version); err != nil {
						return fmt.Errorf("writing version: %w", err)
					}

					return nil
				},
			},
		},
	}, nil
}
