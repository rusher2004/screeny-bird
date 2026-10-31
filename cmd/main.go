// Command screeny validates, syncs and serves the bird plate catalog.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rusher2004/screeny-bird/commands"
)

func main() {
	err := run(context.Background(), os.Args, os.Stdout)
	os.Exit(report(os.Stderr, err))
}

// run executes the command line, writing command output to w.
func run(ctx context.Context, args []string, w io.Writer) error {
	cmd, err := commands.New(w)
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
