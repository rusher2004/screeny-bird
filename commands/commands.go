// Package commands holds the screeny command tree and its composition root.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rusher2004/screeny-bird/catalog"
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

// ErrMissingSetting reports a setting a command needs that was not supplied.
// An empty value counts as not supplied.
var ErrMissingSetting = errors.New("missing required setting")

// Settings are the operator-supplied values commands use. A command checks the
// ones it needs when it runs, so commands that need none work without any.
type Settings struct {
	// CatalogDir is the directory holding the catalog's index.json and species/.
	CatalogDir string
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

// Options injects everything the command tree depends on. Version, Out and
// ErrOut are required: there are no defaults. Settings carries what the caller
// supplied; each command checks the settings it needs. Catalogs replaces the
// filesystem catalog loader and exists so tests can substitute it.
type Options struct {
	Version  string
	Out      io.Writer
	ErrOut   io.Writer
	Settings Settings
	Catalogs catalogLoader
}

// New builds the root command. Run on the result returns every error,
// including usage errors, and never prints one or exits the process.
func New(opts Options) (*cli.Command, error) {
	switch {
	case opts.Version == "":
		return nil, fmt.Errorf("building command tree: version: %w", ErrMissingOption)
	case opts.Out == nil:
		return nil, fmt.Errorf("building command tree: out: %w", ErrMissingOption)
	case opts.ErrOut == nil:
		return nil, fmt.Errorf("building command tree: errOut: %w", ErrMissingOption)
	}

	catalogs := opts.Catalogs
	if catalogs == nil {
		catalogs = dirCatalogs{}
	}

	return &cli.Command{
		Name:      "screeny",
		Usage:     "Validate, sync and serve the bird plate catalog",
		Writer:    opts.Out,
		ErrWriter: opts.ErrOut,
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
							dir := opts.Settings.CatalogDir
							if strings.TrimSpace(dir) == "" {
								return fmt.Errorf("catalog_dir: %w", ErrMissingSetting)
							}

							species, err := catalogs.LoadCatalog(dir)
							if err != nil {
								return fmt.Errorf("validating catalog %q: %w", dir, err)
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
