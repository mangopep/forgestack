package docgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// APIRoute is one HTTP route extracted directly from source code.
type APIRoute struct {
	Method      string
	Path        string
	StatusCodes []int // observed writeJSON(...) status literals inside the handler
	SourceFile  string
	SourceLine  int
}

// ExtractAPIRoutes parses goFile looking for mux.HandleFunc("METHOD /path", fn)
// or mux.Handle("METHOD /path", handler) registrations (the shape the
// composition root in internal/platform/router uses) and returns every
// route it finds, in source order. When the handler argument is a function
// literal (inline handlers like /healthz), observed writeJSON status codes
// are also extracted; when it's an arbitrary expression (e.g. a module's
// transport.NewXHandler(...) call), only method/path are known — this tool
// does not perform cross-package analysis. It only reports what it can read
// directly from the AST — it never guesses behavior that isn't
// expressed in code.
func ExtractAPIRoutes(goFile string) ([]APIRoute, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goFile, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", goFile, err)
	}

	var routes []APIRoute
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		if len(call.Args) < 2 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		raw, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		method, path, ok := strings.Cut(raw, " ")
		if !ok {
			return true
		}
		pos := fset.Position(call.Pos())
		route := APIRoute{
			Method:     method,
			Path:       path,
			SourceFile: pos.Filename,
			SourceLine: pos.Line,
		}
		if fn, ok := call.Args[1].(*ast.FuncLit); ok {
			route.StatusCodes = extractWriteJSONStatusCodes(fn)
		}
		routes = append(routes, route)
		return true
	})
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].SourceLine < routes[j].SourceLine })
	return routes, nil
}

// httpStatusConstants maps the net/http status constant names this
// generator understands to their numeric value. It is intentionally a
// fixed, explicit list (not a reflection trick) so the mapping is obvious
// to read and update.
var httpStatusConstants = map[string]int{
	"StatusOK":                  200,
	"StatusCreated":             201,
	"StatusNoContent":           204,
	"StatusBadRequest":          400,
	"StatusUnauthorized":        401,
	"StatusForbidden":           403,
	"StatusNotFound":            404,
	"StatusConflict":            409,
	"StatusUnprocessableEntity": 422,
	"StatusTooManyRequests":     429,
	"StatusInternalServerError": 500,
}

// extractWriteJSONStatusCodes walks a handler's function literal looking for
// calls shaped like writeJSON(w, <status>, ...), where <status> is either
// an integer literal or a recognized net/http status constant, and
// collects the distinct status codes actually written in that handler's
// source.
func extractWriteJSONStatusCodes(fn *ast.FuncLit) []int {
	seen := map[int]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "writeJSON" || len(call.Args) < 2 {
			return true
		}
		if code, ok := literalStatusCode(call.Args[1]); ok {
			seen[code] = true
		}
		return true
	})
	var codes []int
	for c := range seen {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	return codes
}

// literalStatusCode extracts a status code from either an integer literal
// (e.g. 200) or a recognized http.StatusXxx selector expression.
func literalStatusCode(arg ast.Expr) (int, bool) {
	switch v := arg.(type) {
	case *ast.BasicLit:
		if v.Kind != token.INT {
			return 0, false
		}
		code, err := strconv.Atoi(v.Value)
		if err != nil {
			return 0, false
		}
		return code, true
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		if !ok || pkg.Name != "http" {
			return 0, false
		}
		code, ok := httpStatusConstants[v.Sel.Name]
		return code, ok
	default:
		return 0, false
	}
}

// RenderAPIMarkdown renders routes as a Markdown table, with a header
// explaining that this file is generated and must not be hand-edited.
func RenderAPIMarkdown(routes []APIRoute, sourceRelPath string) string {
	var b strings.Builder
	b.WriteString("<!-- GENERATED FILE. Do not edit by hand. -->\n")
	b.WriteString("<!-- Regenerate with: go run ./cmd/docgen generate api -->\n\n")
	b.WriteString("# API Reference (generated)\n\n")
	fmt.Fprintf(&b, "Extracted directly from `%s` by parsing the Go source (`go/ast`) — not hand-written, not AI-generated. If this file is stale relative to the source, regenerate it; the CI docs check fails the build if it's out of sync.\n\n", sourceRelPath)
	b.WriteString("| Method | Path | Observed status codes | Source |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, r := range routes {
		codes := "unknown"
		if len(r.StatusCodes) > 0 {
			parts := make([]string, len(r.StatusCodes))
			for i, c := range r.StatusCodes {
				parts[i] = strconv.Itoa(c)
			}
			codes = strings.Join(parts, ", ")
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s:%d |\n", r.Method, r.Path, codes, sourceRelPath, r.SourceLine)
	}
	b.WriteString("\n\"Observed status codes\" lists every status literal passed to `writeJSON` inside that handler's source, in ascending order — it is not a claim about which codes occur in production traffic, only which codes the code is capable of returning.\n")
	return b.String()
}
