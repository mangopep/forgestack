// Command docgen is ForgeStack's deterministic documentation tool: it
// detects which documentation areas a set of changed files affects, and
// regenerates the documentation that can be produced directly from source
// (API routes, CI workflow inventory). It calls no AI provider and makes no
// network calls — every result is reproducible from the files already in
// this repository.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mangopep/forgestack/internal/docgen"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "impact":
		err = runImpact(os.Args[2:])
	case "generate":
		err = runGenerate(os.Args[2:])
	case "checklinks":
		err = runCheckLinks(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  docgen impact --base <ref> --head <ref>   report which doc areas changed files impact (JSON on stdout)
  docgen generate [api|ci|all]              regenerate deterministic docs under docs/generated/
  docgen checklinks [dir]                   fail if any relative Markdown link under dir is broken`)
}

func runImpact(args []string) error {
	base, head := "origin/main", "HEAD"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			i++
			base = args[i]
		case "--head":
			i++
			head = args[i]
		}
	}
	changed, err := docgen.ChangedFiles(".", base, head)
	if err != nil {
		return err
	}
	m, err := docgen.LoadImpactMap("docs/doc-impact-map.json")
	if err != nil {
		return err
	}
	report, err := docgen.Match(m, changed)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func runGenerate(args []string) error {
	target := "all"
	if len(args) > 0 {
		target = args[0]
	}
	if target == "api" || target == "all" {
		if err := generateAPI(); err != nil {
			return err
		}
	}
	if target == "ci" || target == "all" {
		if err := generateCI(); err != nil {
			return err
		}
	}
	return nil
}

func generateAPI() error {
	const src = "internal/api/api.go"
	routes, err := docgen.ExtractAPIRoutes(src)
	if err != nil {
		return err
	}
	md := docgen.RenderAPIMarkdown(routes, src)
	if err := os.MkdirAll("docs/generated", 0o755); err != nil {
		return err
	}
	return os.WriteFile("docs/generated/API.md", []byte(md), 0o644)
}

func generateCI() error {
	summaries, err := docgen.SummarizeWorkflows(".github/workflows")
	if err != nil {
		return err
	}
	md := docgen.RenderCIMarkdown(summaries)
	if err := os.MkdirAll("docs/generated", 0o755); err != nil {
		return err
	}
	return os.WriteFile("docs/generated/CI_WORKFLOWS.md", []byte(md), 0o644)
}

func runCheckLinks(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	broken, err := docgen.CheckLinks(dir)
	if err != nil {
		return err
	}
	if len(broken) == 0 {
		fmt.Println("checklinks: all relative Markdown links resolve")
		return nil
	}
	for _, b := range broken {
		fmt.Fprintf(os.Stderr, "%s:%d: broken link target %q\n", b.File, b.Line, b.Target)
	}
	return fmt.Errorf("%d broken link(s) found", len(broken))
}
