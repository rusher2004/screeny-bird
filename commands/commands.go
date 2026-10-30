// Package commands holds the screeny command tree and its composition root.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"
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

// Options injects everything the command tree depends on. Every field is
// required: there are no defaults.
type Options struct {
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
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
