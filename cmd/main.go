// Command screeny validates, syncs and serves the bird plate catalog.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rusher2004/screeny-bird/commands"
)

// version is injected at build time with -ldflags "-X main.version=<version>".
var version string

func main() {
	os.Exit(run(context.Background(), version, environMap(os.Environ()), os.Args, os.Stdout, os.Stderr))
}

// environMap turns KEY=VALUE entries into a map. main is the only code that
// reads the process environment; everything else receives this map.
func environMap(entries []string) map[string]string {
	env := make(map[string]string, len(entries))

	for _, entry := range entries {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
		}
	}

	return env
}

// run executes the command line and returns the process exit code. It is the
// one place errors are printed and exit codes are chosen.
func run(ctx context.Context, version string, env map[string]string, args []string, stdout, stderr io.Writer) int {
	cmd, err := commands.New(commands.Options{Version: version, Stdout: stdout, Stderr: stderr, Env: env})
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
