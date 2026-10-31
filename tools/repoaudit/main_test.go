package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating directory for %s: %v", name, err)
		}

		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return root
}

func kinds(fs []finding) map[string][]string {
	got := map[string][]string{}
	for _, f := range fs {
		got[f.Kind] = append(got[f.Kind], f.Path)
	}

	return got
}

func TestAuditCleanTree(t *testing.T) {
	t.Parallel()

	root := writeTree(t, map[string]string{
		"catalog/catalog.go": "package catalog\n\ntype Species struct{ ID int }\n\nfunc New() Species { return Species{} }\n",
		"commands/commands.go": "package commands\n\n" +
			"type reader interface{ Read() error }\n\nfunc use(r reader) error { return r.Read() }\n",
		"cmd/main.go": "package main\n\nimport \"os\"\n\nfunc main() { _ = os.Getenv(\"X\") }\n",
		// A route the spec describes, including a prefix route written with a
		// trailing slash and documented with a path parameter.
		"server/handler.go": "package server\n\nconst (\n\thealthPath = \"/healthz\"\n\tassetsPath = \"/v1/assets/\"\n)\n",
		"api/openapi.yaml":  "openapi: 3.0.3\npaths:\n  /healthz:\n    get: {}\n  /v1/assets/{path}:\n    get: {}\n",
	})

	got, err := audit(root)
	if err != nil {
		t.Fatalf("audit() error = %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("audit() = %v, want no findings", got)
	}
}

func TestAuditReportsViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		kind  string
		path  string
	}{
		{
			name: "type-only file",
			files: map[string]string{
				"catalog/types.go": "package catalog\n\ntype Species struct{ ID int }\n",
			},
			kind: kindTypeOnlyFile,
			path: "catalog/types.go",
		},
		{
			name: "iface file name",
			files: map[string]string{
				"commands/reader_iface.go": "package commands\n\nfunc F() {}\n",
			},
			kind: kindTypeOnlyFile,
			path: "commands/reader_iface.go",
		},
		{
			name: "env read outside cmd",
			files: map[string]string{
				"commands/commands.go": "package commands\n\nimport \"os\"\n\nfunc F() string { return os.Getenv(\"X\") }\n",
			},
			kind: kindAmbientAccess,
			path: "commands/commands.go",
		},
		{
			name: "aliased os import",
			files: map[string]string{
				"fsutil/fsutil.go": "package fsutil\n\nimport stdos \"os\"\n\nfunc F() (string, error) { return stdos.UserCacheDir() }\n",
			},
			kind: kindAmbientAccess,
			path: "fsutil/fsutil.go",
		},
		{
			name: "env read in a test file",
			files: map[string]string{
				"catalog/catalog.go":      "package catalog\n\nfunc F() {}\n",
				"catalog/catalog_test.go": "package catalog_test\n\nimport \"os\"\n\nvar _ = os.Environ\n",
			},
			kind: kindAmbientAccess,
			path: "catalog/catalog_test.go",
		},
		{
			name: "whole environment read in cmd",
			files: map[string]string{
				"cmd/main.go": "package main\n\nimport \"os\"\n\nfunc main() { _ = os.Environ() }\n",
			},
			kind: kindAmbientAccess,
			path: "cmd/main.go",
		},
		{
			name: "implicit directory in cmd",
			files: map[string]string{
				"cmd/main.go": "package main\n\nimport \"os\"\n\nfunc main() { _, _ = os.UserConfigDir() }\n",
			},
			kind: kindAmbientAccess,
			path: "cmd/main.go",
		},
		{
			name: "route missing from the spec",
			files: map[string]string{
				"server/handler.go": "package server\n\nconst (\n\thealthPath = \"/healthz\"\n\tcatalogPath = \"/v1/catalog\"\n)\n",
				"api/openapi.yaml":  "openapi: 3.0.3\npaths:\n  /healthz:\n    get: {}\n",
			},
			kind: kindUndocumentedRoute,
			path: "server/handler.go",
		},
		{
			name: "route with no spec at all",
			files: map[string]string{
				"server/handler.go": "package server\n\nconst healthPath = \"/healthz\"\n",
			},
			kind: kindUndocumentedRoute,
			path: "server/handler.go",
		},
		{
			name: "flag default value",
			files: map[string]string{
				"commands/commands.go": "package commands\n\nimport \"github.com/urfave/cli/v3\"\n\n" +
					"var f = &cli.StringFlag{Name: \"listen\", Value: \":8080\"}\n",
			},
			kind: kindFlagDefault,
			path: "commands/commands.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := audit(writeTree(t, tt.files))
			if err != nil {
				t.Fatalf("audit() error = %v", err)
			}

			paths := kinds(got)[tt.kind]
			if len(paths) != 1 || paths[0] != tt.path {
				t.Fatalf("audit() %s findings = %v, want [%s] (all: %v)", tt.kind, paths, tt.path, got)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		args       func(root string) []string
		wantCode   int
		wantReport string
	}{
		{
			name:  "clean tree",
			files: map[string]string{"catalog/catalog.go": "package catalog\n\nfunc F() {}\n"},
			args:  func(root string) []string { return []string{"-root", root} },
		},
		{
			name:     "findings exit one",
			files:    map[string]string{"catalog/types.go": "package catalog\n\ntype T struct{}\n"},
			args:     func(root string) []string { return []string{"-root", root} },
			wantCode: 1,
		},
		{
			name:       "missing root is a usage error",
			files:      map[string]string{},
			args:       func(string) []string { return nil },
			wantCode:   2,
			wantReport: "repoaudit: usage: -root is required\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := writeTree(t, tt.files)

			var output, errText bytes.Buffer

			err := run(tt.args(root), &output)

			code := report(&errText, err)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (output %q, reported %q)", code, tt.wantCode, output.String(), errText.String())
			}

			if tt.wantReport != "" && errText.String() != tt.wantReport {
				t.Fatalf("reported error = %q, want %q", errText.String(), tt.wantReport)
			}

			if tt.wantCode == 1 && !strings.Contains(output.String(), kindTypeOnlyFile) {
				t.Fatalf("output = %q, want it to report %s", output.String(), kindTypeOnlyFile)
			}
		})
	}
}

// TestRepository is the repo-wide check: it fails the build when this
// repository breaks a rule the audit enforces.
func TestRepository(t *testing.T) {
	t.Parallel()

	got, err := audit(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("audit(repository) error = %v", err)
	}

	for _, f := range got {
		t.Errorf("%s", f)
	}
}
