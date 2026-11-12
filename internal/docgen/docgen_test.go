package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchFile(t *testing.T) {
	cases := []struct {
		pattern, file string
		want          bool
	}{
		{"internal/api/**/*.go", "internal/api/api.go", true},
		{"internal/api/**/*.go", "internal/api/sub/handler.go", true},
		{"internal/api/**/*.go", "internal/other/thing.go", false},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/**", "README.md", false},
		{"Dockerfile", "Dockerfile", true},
		{"Dockerfile", "cmd/Dockerfile", true}, // bare filename matches by basename
		{"Dockerfile", "Dockerfile.dev", false},
	}
	for _, c := range cases {
		rule := ImpactRule{Pattern: c.pattern, Area: "x", DocPath: "y", Kind: "manual"}
		got, err := rule.MatchFile(c.file)
		if err != nil {
			t.Fatalf("pattern %q file %q: unexpected error: %v", c.pattern, c.file, err)
		}
		if got != c.want {
			t.Errorf("pattern %q file %q: got %v, want %v", c.pattern, c.file, got, c.want)
		}
	}
}

func TestMatchReport(t *testing.T) {
	m := ImpactMap{Rules: []ImpactRule{
		{Pattern: "internal/api/**/*.go", Area: "API reference", DocPath: "docs/generated/API.md", Kind: "generated"},
		{Pattern: "databases/**", Area: "Database schema", DocPath: "docs/DATABASE.md", Kind: "manual"},
	}}

	t.Run("unrelated change produces no impact", func(t *testing.T) {
		report, err := Match(m, []string{"go.mod"})
		if err != nil {
			t.Fatal(err)
		}
		if report.RequiresDocUpdate {
			t.Error("unrelated change should not require a doc update")
		}
		if len(report.Impacted) != 0 {
			t.Errorf("expected no impacted areas, got %+v", report.Impacted)
		}
	})

	t.Run("api change is detected", func(t *testing.T) {
		report, err := Match(m, []string{"internal/api/api.go", "go.mod"})
		if err != nil {
			t.Fatal(err)
		}
		if !report.RequiresDocUpdate {
			t.Fatal("expected RequiresDocUpdate=true")
		}
		if len(report.Impacted) != 1 {
			t.Fatalf("expected exactly 1 impacted area, got %d: %+v", len(report.Impacted), report.Impacted)
		}
		if report.Impacted[0].Kind != "generated" {
			t.Errorf("expected kind=generated, got %s", report.Impacted[0].Kind)
		}
	})
}

func TestLoadImpactMapRejectsBadConfig(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badFile, []byte(`{"rules":[{"pattern":"x","area":"y","doc_path":"z","kind":"not-a-real-kind"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImpactMap(badFile); err == nil {
		t.Error("expected error for unknown kind, got nil")
	}
}

func TestLoadRealImpactMap(t *testing.T) {
	// Exercises the actual config shipped in this repo, not a fixture, so a
	// broken docs/doc-impact-map.json fails this test immediately.
	m, err := LoadImpactMap(filepath.Join("..", "..", "docs", "doc-impact-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rules) == 0 {
		t.Fatal("expected at least one rule in the real impact map")
	}
}

func TestExtractAPIRoutesFromRealSource(t *testing.T) {
	// Exercises the actual internal/api/api.go, not a fixture, so this test
	// fails the moment the generator can no longer read real route
	// registrations correctly.
	routes, err := ExtractAPIRoutes(filepath.Join("..", "api", "api.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"GET /healthz":       false,
		"GET /readyz":        false,
		"GET /api/v1/posts":  false,
		"POST /api/v1/posts": false,
		"GET /metrics-demo":  false,
	}
	for _, r := range routes {
		want[r.Method+" "+r.Path] = true
	}
	for k, found := range want {
		if !found {
			t.Errorf("expected route %q to be extracted, it was not found among %d routes", k, len(routes))
		}
	}
	for _, r := range routes {
		if r.Method+" "+r.Path == "POST /api/v1/posts" {
			if len(r.StatusCodes) == 0 {
				t.Error("expected POST /api/v1/posts to have observed status codes extracted")
			}
		}
	}
}

func TestRenderAPIMarkdownIsMarkedGenerated(t *testing.T) {
	md := RenderAPIMarkdown([]APIRoute{{Method: "GET", Path: "/x", SourceFile: "f.go", SourceLine: 1}}, "f.go")
	if !strings.Contains(md, "GENERATED FILE") {
		t.Error("generated markdown must contain a do-not-edit marker")
	}
	if !strings.Contains(md, "`/x`") {
		t.Error("expected route path to appear in rendered markdown")
	}
}

func TestSummarizeWorkflowsFromRealDir(t *testing.T) {
	summaries, err := SummarizeWorkflows(filepath.Join("..", "..", ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) == 0 {
		t.Fatal("expected at least one workflow summary")
	}
	foundCI := false
	for _, s := range summaries {
		if s.File == "ci.yml" {
			foundCI = true
			if s.Name == "" {
				t.Error("ci.yml should have a detected name")
			}
			if len(s.Triggers) == 0 {
				t.Error("ci.yml should have at least one detected trigger")
			}
		}
	}
	if !foundCI {
		t.Error("expected ci.yml to be among the summarized workflows")
	}
}

func TestCheckLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := "[ok](real.md)\n[external](https://example.com)\n[anchor](#section)\n"
	bad := good + "[broken](does-not-exist.md)\n"

	if err := os.WriteFile(filepath.Join(dir, "good.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	broken, err := CheckLinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Fatalf("expected no broken links in good.md, got %+v", broken)
	}

	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	broken, err = CheckLinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 1 {
		t.Fatalf("expected exactly 1 broken link, got %+v", broken)
	}
	if broken[0].Target != "does-not-exist.md" {
		t.Errorf("expected broken target does-not-exist.md, got %q", broken[0].Target)
	}
}
