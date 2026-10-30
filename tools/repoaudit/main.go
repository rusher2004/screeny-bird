// Command repoaudit enforces the repository's mechanical rules with go/ast: no
// type-only files, no ambient configuration access outside cmd/, and no flag
// defaults. Dependency inversion is a design judgment that code cannot check,
// so it is left to review.
//
// Usage: repoaudit -root <repository root>
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	kindTypeOnlyFile  = "type-only-file"
	kindAmbientAccess = "ambient-access"
	kindFlagDefault   = "flag-default"
)

// ambientOSCalls read or change process-wide ambient state. Outside cmd/ every
// one is banned: packages receive their configuration explicitly.
var ambientOSCalls = []string{
	"Getenv", "LookupEnv", "Environ", "ExpandEnv", "Setenv", "Unsetenv", "Clearenv",
	"UserHomeDir", "UserConfigDir", "UserCacheDir",
}

// namedEnvReads are the only ambient calls cmd/ may make. Each reads one key
// that the program declares; handing down the whole environment, expanding
// variables, or deriving implicit directories is banned there too.
var namedEnvReads = []string{"Getenv", "LookupEnv"}

const cliImportPath = "github.com/urfave/cli/v3"

type finding struct {
	Path string
	Line int
	Kind string
	Msg  string
}

func (f finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.Kind, f.Msg)
}

var (
	errUsage    = errors.New("usage")
	errFindings = errors.New("findings reported")
)

func main() {
	err := run(os.Args[1:], os.Stdout)
	os.Exit(report(os.Stderr, err))
}

// run audits the repository named by -root and writes each finding to w.
func run(args []string, w io.Writer) error {
	flags := flag.NewFlagSet("repoaudit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	root := flags.String("root", "", "repository root (required)")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	if *root == "" {
		return fmt.Errorf("%w: -root is required", errUsage)
	}

	all, err := audit(*root)
	if err != nil {
		return err
	}

	for _, f := range all {
		if _, err := fmt.Fprintln(w, f); err != nil {
			return fmt.Errorf("writing findings: %w", err)
		}
	}

	if len(all) > 0 {
		return fmt.Errorf("%d findings: %w", len(all), errFindings)
	}

	return nil
}

// report prints err to w and returns the exit code: 0 for no error, 2 for a
// usage error, 1 otherwise.
func report(w io.Writer, err error) int {
	if err == nil {
		return 0
	}

	// Nothing useful can be done if the writer itself fails; the exit code reports the failure.
	_, _ = fmt.Fprintf(w, "repoaudit: %v\n", err)

	if errors.Is(err, errUsage) {
		return 2
	}

	return 1
}

// audit parses every Go file under root and returns the findings, sorted by
// path and line.
func audit(root string) ([]finding, error) {
	var all []finding

	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", p, err)
		}

		name := d.Name()

		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(name, ".go") {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("making %s relative to %s: %w", p, root, err)
		}

		found, err := auditFile(p, filepath.ToSlash(rel))
		if err != nil {
			return err
		}

		all = append(all, found...)

		return nil
	}

	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, fmt.Errorf("auditing %s: %w", root, err)
	}

	slices.SortFunc(all, func(a, b finding) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}

		return a.Line - b.Line
	})

	return all, nil
}

func auditFile(abs, rel string) ([]finding, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rel, err)
	}

	var (
		out    []finding
		isTest = strings.HasSuffix(rel, "_test.go")
		top    = strings.SplitN(rel, "/", 2)[0]
		isCmd  = top == "cmd"
		imps   = importNames(file)
		add    = func(pos token.Pos, kind, msg string) {
			out = append(out, finding{Path: rel, Line: fset.Position(pos).Line, Kind: kind, Msg: msg})
		}
	)

	if !isTest && (isTypeOnly(file) || strings.HasSuffix(rel, "_iface.go")) {
		add(file.Package, kindTypeOnlyFile, "file only declares types; define each type next to the code that uses it")
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if refersTo(node.X, imps, "os") && slices.Contains(ambientOSCalls, node.Sel.Name) {
				switch {
				case !isCmd:
					add(node.Pos(), kindAmbientAccess,
						fmt.Sprintf("os.%s outside cmd/: configuration must be passed in explicitly", node.Sel.Name))
				case !slices.Contains(namedEnvReads, node.Sel.Name):
					add(node.Pos(), kindAmbientAccess,
						fmt.Sprintf("os.%s in cmd/: read only the named settings the program declares with os.Getenv or os.LookupEnv", node.Sel.Name))
				}
			}
		case *ast.CompositeLit:
			if top == "commands" && !isTest && isCLIFlagLiteral(node, imps) {
				for _, elt := range node.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Value" {
							add(kv.Pos(), kindFlagDefault, "flag declares a default Value; configuration has no defaults")
						}
					}
				}
			}
		}

		return true
	})

	return out, nil
}

// importNames maps each import's local name to its import path.
func importNames(file *ast.File) map[string]string {
	names := map[string]string{}

	for _, imp := range file.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue // the parser already rejected malformed import paths
		}

		name := path.Base(importPath)
		if importPath == cliImportPath {
			name = "cli"
		}

		if imp.Name != nil {
			name = imp.Name.Name
		}

		names[name] = importPath
	}

	return names
}

func refersTo(expr ast.Expr, imps map[string]string, importPath string) bool {
	id, ok := expr.(*ast.Ident)

	return ok && imps[id.Name] == importPath
}

func isTypeOnly(file *ast.File) bool {
	hasType := false

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			return false
		}

		switch gen.Tok {
		case token.TYPE:
			hasType = true
		case token.IMPORT:
		default:
			return false
		}
	}

	return hasType
}

func isCLIFlagLiteral(lit *ast.CompositeLit, imps map[string]string) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)

	return ok && refersTo(sel.X, imps, cliImportPath) && strings.HasSuffix(sel.Sel.Name, "Flag")
}
