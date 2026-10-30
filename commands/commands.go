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

// ErrMissingSetting reports a required setting that neither a flag nor the
// injected environment supplied. An empty value counts as not supplied.
var ErrMissingSetting = errors.New("missing required setting")

// Setting names. Each setting is a flag with a matching environment key.
const (
	catalogFlag = "catalog"
	catalogEnv  = "SCREENY_CATALOG"
)

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

// envSource reads one key from the injected environment map. The command tree
// never reads the process environment itself: main passes the map in.
type envSource struct {
	key string
	env map[string]string
}

func (s envSource) Lookup() (string, bool) {
	value := s.env[s.key]

	return value, value != ""
}

func (s envSource) String() string   { return fmt.Sprintf("environment variable %q", s.key) }
func (s envSource) GoString() string { return fmt.Sprintf("envSource{key: %q}", s.key) }
func (s envSource) IsFromEnv() bool  { return true }
func (s envSource) Key() string      { return s.key }

// requiredSetting returns a setting's value, or ErrMissingSetting naming both
// ways to supply it.
func requiredSetting(cmd *cli.Command, flag, envKey string) (string, error) {
	value := cmd.String(flag)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: pass --%s or set %s", ErrMissingSetting, flag, envKey)
	}

	return value, nil
}

// Options injects everything the command tree depends on. Version, Stdout and
// Stderr are required: there are no defaults. Env holds the environment values
// main chose to pass down; nil means none. Catalogs replaces the filesystem
// catalog loader and exists so tests can substitute it.
type Options struct {
	Version  string
	Stdout   io.Writer
	Stderr   io.Writer
	Env      map[string]string
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

	loader := opts.Catalogs
	if loader == nil {
		loader = dirCatalogs{}
	}

	return &cli.Command{
		Name:      "screeny",
		Usage:     "Validate, sync and serve the bird plate catalog",
		Writer:    opts.Stdout,
		ErrWriter: opts.Stderr,
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
								Name:    catalogFlag,
								Usage:   "directory holding index.json and species/ (required)",
								Sources: cli.NewValueSourceChain(envSource{key: catalogEnv, env: opts.Env}),
							},
						},
						Action: func(_ context.Context, cmd *cli.Command) error {
							dir, err := requiredSetting(cmd, catalogFlag, catalogEnv)
							if err != nil {
								return err
							}

							species, err := loader.LoadCatalog(dir)
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
