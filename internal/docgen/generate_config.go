package docgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// EnvVar is one environment variable read directly from Go source via
// os.Getenv or os.LookupEnv.
type EnvVar struct {
	Name       string
	SourceFile string
	SourceLine int
}

// ExtractEnvVars walks every *.go file (excluding _test.go) under root and
// collects every os.Getenv("X") / os.LookupEnv("X") call it finds, where X
// is a string literal. A non-literal argument (e.g. a variable) is skipped
// — this tool reports only what it can read directly from the AST, never a
// guess about what name a variable might hold at runtime.
func ExtractEnvVars(root string) ([]EnvVar, error) {
	var out []EnvVar
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		vars, err := extractEnvVarsFromFile(path)
		if err != nil {
			return err
		}
		out = append(out, vars...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].SourceFile < out[j].SourceFile
	})
	return out, nil
}

func extractEnvVarsFromFile(file string) ([]EnvVar, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}

	var vars []EnvVar
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			return true
		}
		if sel.Sel.Name != "Getenv" && sel.Sel.Name != "LookupEnv" {
			return true
		}
		if len(call.Args) < 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		pos := fset.Position(call.Pos())
		vars = append(vars, EnvVar{Name: name, SourceFile: pos.Filename, SourceLine: pos.Line})
		return true
	})
	return vars, nil
}

// RenderConfigMarkdown renders the environment-variable reference as
// Markdown, deduplicating a variable read from multiple sites into one row
// listing every site.
func RenderConfigMarkdown(vars []EnvVar) string {
	type row struct {
		name  string
		sites []string
	}
	bySite := map[string]*row{}
	var order []string
	for _, v := range vars {
		r, ok := bySite[v.Name]
		if !ok {
			r = &row{name: v.Name}
			bySite[v.Name] = r
			order = append(order, v.Name)
		}
		r.sites = append(r.sites, fmt.Sprintf("%s:%d", v.SourceFile, v.SourceLine))
	}
	sort.Strings(order)

	var b strings.Builder
	b.WriteString("<!-- GENERATED FILE. Do not edit by hand. -->\n")
	b.WriteString("<!-- Regenerate with: go run ./cmd/docgen generate config -->\n\n")
	b.WriteString("# Environment Variable Reference (generated)\n\n")
	b.WriteString("Extracted directly from every `os.Getenv`/`os.LookupEnv` call with a string-literal argument, under `cmd/` and `internal/` — not hand-written. A variable read with a non-literal argument (built at runtime) would not appear here; this is a reference to what the current source reads, not an exhaustive runtime guarantee.\n\n")
	if len(order) == 0 {
		b.WriteString("No environment variables are currently read via `os.Getenv`/`os.LookupEnv` with a literal name.\n")
		return b.String()
	}
	b.WriteString("| Variable | Read at |\n")
	b.WriteString("|---|---|\n")
	for _, name := range order {
		fmt.Fprintf(&b, "| `%s` | %s |\n", name, strings.Join(bySite[name].sites, ", "))
	}
	return b.String()
}
