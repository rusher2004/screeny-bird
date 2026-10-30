package commands_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/commands"
)

func run(t *testing.T, opts commands.Options, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	opts.Stdout = &out
	opts.Stderr = &errOut

	cmd, err := commands.New(opts)
	if err != nil {
		return "", "", err
	}

	err = cmd.Run(context.Background(), append([]string{"screeny"}, args...))

	return out.String(), errOut.String(), err
}

func TestNewRejectsMissingOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]commands.Options{
		"version": {Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}},
		"stdout":  {Version: "1.2.3", Stderr: &bytes.Buffer{}},
		"stderr":  {Version: "1.2.3", Stdout: &bytes.Buffer{}},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd, err := commands.New(opts)
			if err == nil {
				t.Fatalf("New() = %v, want error for missing %s", cmd, name)
			}

			if !errors.Is(err, commands.ErrMissingOption) {
				t.Fatalf("New() error = %v, want ErrMissingOption", err)
			}

			if !strings.Contains(err.Error(), name) {
				t.Fatalf("New() error = %q, want it to name %q", err, name)
			}
		})
	}
}

func TestVersionPrintsVersion(t *testing.T) {
	t.Parallel()

	stdout, _, err := run(t, commands.Options{Version: "1.2.3"}, "version")
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}

	if got, want := stdout, "1.2.3\n"; got != want {
		t.Fatalf("Run(version) stdout = %q, want %q", got, want)
	}
}

func TestUsageErrorsCarryExitCodeTwo(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"unknown flag":    {"--no-such-flag"},
		"unknown command": {"no-such-command"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := run(t, commands.Options{Version: "1.2.3"}, args...)
			if err == nil {
				t.Fatal("Run() error = nil, want usage error")
			}

			exitErr, ok := errors.AsType[*commands.ExitError](err)
			if !ok {
				t.Fatalf("Run() error = %v, want *commands.ExitError in chain", err)
			}

			if exitErr.Code != commands.ExitUsage {
				t.Fatalf("exit code = %d, want %d", exitErr.Code, commands.ExitUsage)
			}
		})
	}
}
