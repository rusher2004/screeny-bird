// Command ifaceaudit enforces the repository's structural rules with go/ast:
// consumer-defined interfaces, no type-only files, and explicit configuration.
//
// Usage: ifaceaudit -root <repository root>
package main

import (
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
	kindTypeOnlyFile      = "type-only-file"
	kindProducerInterface = "producer-interface"
	kindAmbientAccess     = "ambient-access"
	kindFlagDefault       = "flag-default"
	kindProducerTypeUse   = "producer-type-use"
)

// producerPackages are the packages that export concrete types for callers.
// They must declare no interfaces, and callers should reach them through
// small consumer-defined interfaces instead of their concrete types.
var producerPackages = []string{"catalog", "server", "upstream", "derive", "fsutil"}

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

// violations drops the informational findings, leaving those that fail the build.
func violations(all []finding) []finding {
	var out []finding

	for _, f := range all {
		if f.Kind != kindProducerTypeUse {
			out = append(out, f)
		}
	}

	return out
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ifaceaudit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	root := flags.String("root", "", "repository root (required)")

	if err := flags.Parse(args); err != nil {
		return reportUsage(stderr, fmt.Sprintf("%v", err))
	}

	if *root == "" {
		return reportUsage(stderr, "-root is required")
	}

	all, err := audit(*root)
	if err != nil {
		// Nothing useful can be done if stderr is unwritable; the exit code reports the failure.
		_, _ = fmt.Fprintf(stderr, "ifaceaudit: %v\n", err)

		return 1
	}

	for _, f := range all {
		if _, err := fmt.Fprintln(stdout, f); err != nil {
			return 1
		}
	}

	if len(violations(all)) > 0 {
		return 1
	}

	return 0
}

func reportUsage(stderr io.Writer, msg string) int {
	// Nothing useful can be done if stderr is unwritable; the exit code reports the failure.
	_, _ = fmt.Fprintf(stderr, "ifaceaudit: %s\n", msg)

	return 2
}

// audit parses every Go file under root and returns the findings, sorted by
// path and line.
func audit(root string) ([]finding, error) {
	module, err := readModulePath(root)
	if err != nil {
		return nil, fmt.Errorf("auditing %s: %w", root, err)
	}

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

		found, err := auditFile(p, filepath.ToSlash(rel), module)
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

func readModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}

	return "", fmt.Errorf("go.mod under %s has no module line", root)
}

func auditFile(abs, rel, module string) ([]finding, error) {
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

	inProducer := !isTest && slices.Contains(producerPackages, top) && path.Dir(rel) == top
	if inProducer {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}

			for _, spec := range gen.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					if _, isIface := ts.Type.(*ast.InterfaceType); isIface {
						add(ts.Pos(), kindProducerInterface,
							fmt.Sprintf("producer package %s declares interface %s; consumers declare their own", top, ts.Name.Name))
					}
				}
			}
		}
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
		case *ast.Field:
			if !inProducer && !isTest {
				if pkg, name, ok := producerTypeRef(node.Type, imps, module); ok {
					add(node.Pos(), kindProducerTypeUse,
						fmt.Sprintf("concrete producer type %s.%s; accept a small consumer-defined interface", pkg, name))
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

// producerTypeRef reports whether expr names a type from a producer package.
func producerTypeRef(expr ast.Expr, imps map[string]string, module string) (pkg, name string, ok bool) {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.Ellipsis:
			expr = e.Elt
		case *ast.ArrayType:
			expr = e.Elt
		case *ast.SelectorExpr:
			id, isIdent := e.X.(*ast.Ident)
			if !isIdent {
				return "", "", false
			}

			importPath := imps[id.Name]
			for _, producer := range producerPackages {
				if importPath == module+"/"+producer {
					return id.Name, e.Sel.Name, true
				}
			}

			return "", "", false
		default:
			return "", "", false
		}
	}
}
