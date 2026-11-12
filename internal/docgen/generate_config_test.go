package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractEnvVars(t *testing.T) {
	dir := t.TempDir()
	src := `package x
import "os"
func f() {
	a := os.Getenv("FOO")
	b, _ := os.LookupEnv("BAR")
	_ = a
	_ = b
	name := "BAZ"
	_ = os.Getenv(name) // non-literal argument: must be skipped, not guessed
}
`
	goFile := filepath.Join(dir, "x.go")
	if err := os.WriteFile(goFile, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// also a _test.go file in the same dir, which must be excluded by the walker
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(`package x
import "os"
func testOnly() { os.Getenv("SHOULD_NOT_APPEAR") }
`), 0o600); err != nil {
		t.Fatal(err)
	}

	vars, err := ExtractEnvVars(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range vars {
		names[v.Name] = true
	}
	if !names["FOO"] || !names["BAR"] {
		t.Fatalf("expected FOO and BAR to be extracted, got %+v", vars)
	}
	if names["BAZ"] {
		t.Error("a non-literal os.Getenv argument must not be resolved to its variable's current value")
	}
	if names["SHOULD_NOT_APPEAR"] {
		t.Error("_test.go files must be excluded from extraction")
	}
}

func TestExtractEnvVarsInvalidGoFileFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("this is not valid go {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractEnvVars(dir); err == nil {
		t.Error("expected a parse error for an invalid .go file, not a silently empty result")
	}
}

func TestExtractEnvVarsFromRealSource(t *testing.T) {
	// Exercises the actual cmd/ tree, not a fixture, so a change to the real
	// env-var surface is reflected here without needing a parallel fixture
	// update.
	vars, err := ExtractEnvVars(filepath.Join("..", "..", "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vars {
		if v.Name == "PORT" {
			found = true
		}
	}
	if !found {
		t.Error("expected PORT (read in cmd/api/main.go) to be extracted from the real cmd/ tree")
	}
}

func TestRenderConfigMarkdownIsMarkedGenerated(t *testing.T) {
	md := RenderConfigMarkdown([]EnvVar{{Name: "FOO", SourceFile: "f.go", SourceLine: 3}})
	if !strings.Contains(md, "GENERATED FILE") {
		t.Error("generated markdown must contain a do-not-edit marker")
	}
	if !strings.Contains(md, "`FOO`") {
		t.Error("expected variable name to appear in rendered markdown")
	}
}

func TestRenderConfigMarkdownHandlesNoVars(t *testing.T) {
	md := RenderConfigMarkdown(nil)
	if !strings.Contains(md, "No environment variables") {
		t.Error("expected an explicit note when no env vars are found, not a silently empty table")
	}
}

func TestRenderConfigMarkdownDeduplicatesMultipleSites(t *testing.T) {
	md := RenderConfigMarkdown([]EnvVar{
		{Name: "FOO", SourceFile: "a.go", SourceLine: 1},
		{Name: "FOO", SourceFile: "b.go", SourceLine: 2},
	})
	if strings.Count(md, "| `FOO` |") != 1 {
		t.Errorf("expected FOO to appear in exactly one row listing both sites, got:\n%s", md)
	}
	if !strings.Contains(md, "a.go:1") || !strings.Contains(md, "b.go:2") {
		t.Error("expected both source sites to be listed in FOO's row")
	}
}
