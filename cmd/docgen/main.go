// Command docgen is ForgeStack's deterministic documentation tool: it
// detects which documentation areas a set of changed files affects, and
// regenerates the documentation that can be produced directly from source
// (API routes, CI workflow inventory). It calls no AI provider and makes no
// network calls — every result is reproducible from the files already in
// this repository.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

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
	case "check":
		err = runCheck()
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
  docgen impact --base <ref> --head <ref>        report which doc areas changed files impact (JSON on stdout)
  docgen generate [api|ci|commands|config|all]   regenerate deterministic docs under docs/generated/
  docgen checklinks [dir]                        fail if any relative Markdown link under dir is broken
  docgen check                                   local convenience: regenerate + checklinks, fail if generated docs are now stale (same check CI runs, for before you push)`)
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
	if target == "commands" || target == "all" {
		if err := generateCommands(); err != nil {
			return err
		}
	}
	if target == "config" || target == "all" {
		if err := generateConfig(); err != nil {
			return err
		}
	}
	return nil
}

func generateAPI() error {
	const src = "internal/platform/router/router.go"
	routes, err := docgen.ExtractAPIRoutes(src)
	if err != nil {
		return err
	}
	md := docgen.RenderAPIMarkdown(routes, src)
	if err := os.MkdirAll("docs/generated", 0o750); err != nil {
		return err
	}
	return os.WriteFile("docs/generated/API.md", []byte(md), 0o600)
}

func generateCI() error {
	summaries, err := docgen.SummarizeWorkflows(".github/workflows")
	if err != nil {
		return err
	}
	md := docgen.RenderCIMarkdown(summaries)
	if err := os.MkdirAll("docs/generated", 0o750); err != nil {
		return err
	}
	return os.WriteFile("docs/generated/CI_WORKFLOWS.md", []byte(md), 0o600)
}

func generateCommands() error {
	const src = "Makefile"
	cmds, err := docgen.ExtractMakefileCommands(src)
	if err != nil {
		return err
	}
	md := docgen.RenderCommandsMarkdown(cmds, src)
	if err := os.MkdirAll("docs/generated", 0o750); err != nil {
		return err
	}
	return os.WriteFile("docs/generated/COMMANDS.md", []byte(md), 0o600)
}

func generateConfig() error {
	var vars []docgen.EnvVar
	for _, root := range []string{"cmd", "internal"} {
		v, err := docgen.ExtractEnvVars(root)
		if err != nil {
			return err
		}
		vars = append(vars, v...)
	}
	md := docgen.RenderConfigMarkdown(vars)
	if err := os.MkdirAll("docs/generated", 0o750); err != nil {
		return err
	}
	return os.WriteFile("docs/generated/CONFIG.md", []byte(md), 0o600)
}

// runCheck is the local, pre-push equivalent of what docs-check.yml's CI
// job already does: regenerate every deterministic doc, then fail if that
// changed anything relative to what's committed, and separately run
// checklinks. It shells out to `git diff` rather than reimplementing a
// working-tree diff, matching the CI job's own approach exactly so the two
// can never silently disagree about what "stale" means.
func runCheck() error {
	if err := runGenerate([]string{"all"}); err != nil {
		return err
	}
	cmd := exec.Command("git", "diff", "--exit-code", "--", "docs/generated")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "docgen: generated docs under docs/generated/ are stale — run 'go run ./cmd/docgen generate all' and commit the result")
		if stderr.Len() > 0 {
			fmt.Fprint(os.Stderr, stderr.String())
		}
		return fmt.Errorf("generated docs are stale")
	}
	fmt.Println("docgen check: generated docs are up to date")
	return runCheckLinks(nil)
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
