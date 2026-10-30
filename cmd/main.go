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

// version is injected at build time with -ldflags "-X main.version=<version>".
var version string

func main() {
	os.Exit(run(context.Background(), version, configFromEnv(os.Getenv), os.Args, os.Stdout, os.Stderr))
}

// configFromEnv reads exactly the settings the program declares, and nothing
// else, from the environment. main is the only code that reads the process
// environment; everything else receives this Config.
func configFromEnv(getenv func(string) string) commands.Config {
	return commands.Config{
		Catalog: getenv(commands.EnvCatalog),
	}
}

// run executes the command line and returns the process exit code. It is the
// one place errors are printed and exit codes are chosen.
func run(ctx context.Context, version string, cfg commands.Config, args []string, stdout, stderr io.Writer) int {
	cmd, err := commands.New(commands.Options{Version: version, Stdout: stdout, Stderr: stderr, Config: cfg})
	if err == nil {
		err = cmd.Run(ctx, args)
	}

	if err == nil {
		return 0
	}

	// Nothing useful can be done if stderr itself is unwritable, and the exit
	// code below already reports the failure.
	_, _ = fmt.Fprintf(stderr, "screeny: %v\n", err)

	if exitErr, ok := errors.AsType[*commands.ExitError](err); ok {
		return exitErr.Code
	}

	return 1
}
