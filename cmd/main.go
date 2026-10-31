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

func main() {
	err := run(context.Background(), os.Args, os.Stdout)
	os.Exit(report(os.Stderr, err))
}

// run wires the real config and catalog loaders and executes the command
// line, writing command output to w.
func run(ctx context.Context, args []string, w io.Writer) error {
	cmd, err := commands.New(commands.Options{
		Output:           w,
		NewConfigLoader:  newConfigLoader,
		NewCatalogLoader: newCatalogLoader,
	})
	if err != nil {
		return err
	}

	return cmd.Run(ctx, args)
}

// newConfigLoader adapts config.NewTOMLLoader to the factory commands declares,
// returning an untyped nil on failure.
func newConfigLoader(path string) (commands.ConfigLoader, error) {
	loader, err := config.NewTOMLLoader(path)
	if err != nil {
		return nil, err
	}

	return loader, nil
}

// newCatalogLoader adapts catalog.NewDirLoader to the factory commands
// declares, returning an untyped nil on failure.
func newCatalogLoader(dir string) (commands.CatalogLoader, error) {
	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		return nil, err
	}

	return loader, nil
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
