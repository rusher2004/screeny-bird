package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureModule = "example.test/screeny"

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	files["go.mod"] = "module " + fixtureModule + "\n\ngo 1.27\n"

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
			name: "interface in producer package",
			files: map[string]string{
				"server/server.go": "package server\n\ntype Reader interface{ Read() error }\n\nfunc F() {}\n",
			},
			kind: kindProducerInterface,
			path: "server/server.go",
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

func TestAuditListsProducerTypeUse(t *testing.T) {
	t.Parallel()

	root := writeTree(t, map[string]string{
		"catalog/catalog.go": "package catalog\n\ntype Store struct{}\n\nfunc F() {}\n",
		"commands/commands.go": "package commands\n\nimport \"" + fixtureModule + "/catalog\"\n\n" +
			"func Use(s *catalog.Store) {}\n",
	})

	got, err := audit(root)
	if err != nil {
		t.Fatalf("audit() error = %v", err)
	}

	paths := kinds(got)[kindProducerTypeUse]
	if len(paths) != 1 || paths[0] != "commands/commands.go" {
		t.Fatalf("audit() producer-type-use = %v, want [commands/commands.go]", paths)
	}

	if len(violations(got)) != 0 {
		t.Fatalf("violations() = %v, want none: producer-type-use is informational", violations(got))
	}
}

func TestAuditRejectsMissingModule(t *testing.T) {
	t.Parallel()

	if _, err := audit(t.TempDir()); err == nil {
		t.Fatal("audit() error = nil, want error for a root without go.mod")
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		args       func(root string) []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:  "clean tree",
			files: map[string]string{"catalog/catalog.go": "package catalog\n\nfunc F() {}\n"},
			args:  func(root string) []string { return []string{"-root", root} },
		},
		{
			name:     "violation exits one",
			files:    map[string]string{"catalog/types.go": "package catalog\n\ntype T struct{}\n"},
			args:     func(root string) []string { return []string{"-root", root} },
			wantCode: 1,
		},
		{
			name:       "missing root is a usage error",
			files:      map[string]string{},
			args:       func(string) []string { return nil },
			wantCode:   2,
			wantStderr: "ifaceaudit: -root is required\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := writeTree(t, tt.files)

			var stdout, stderr bytes.Buffer

			code := run(tt.args(root), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run() code = %d, want %d (stdout %q, stderr %q)", code, tt.wantCode, stdout.String(), stderr.String())
			}

			if tt.wantStderr != "" && stderr.String() != tt.wantStderr {
				t.Fatalf("run() stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}

			if tt.wantCode == 1 && !strings.Contains(stdout.String(), kindTypeOnlyFile) {
				t.Fatalf("run() stdout = %q, want it to report %s", stdout.String(), kindTypeOnlyFile)
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

	for _, f := range violations(got) {
		t.Errorf("%s", f)
	}
}
