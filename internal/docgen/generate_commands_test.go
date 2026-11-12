package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractMakefileCommands(t *testing.T) {
	dir := t.TempDir()
	makefile := filepath.Join(dir, "Makefile")
	content := `.PHONY: help build
help:
	@echo 'build'
build:
	go build -o bin/x ./cmd/x
	echo done
internal-rule:
	this is not a .PHONY target and must not appear
`
	if err := os.WriteFile(makefile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cmds, err := ExtractMakefileCommands(makefile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected exactly 2 commands (only .PHONY targets), got %d: %+v", len(cmds), cmds)
	}
	if cmds[0].Target != "help" || cmds[1].Target != "build" {
		t.Errorf("expected declaration order [help, build], got %+v", cmds)
	}
	if len(cmds[1].Run) != 2 {
		t.Fatalf("expected build to have 2 recipe lines, got %+v", cmds[1].Run)
	}
	if cmds[1].Run[0] != "go build -o bin/x ./cmd/x" {
		t.Errorf("expected tab-indented recipe line verbatim, got %q", cmds[1].Run[0])
	}
	if cmds[0].Run[0] != "echo 'build'" {
		t.Errorf("expected leading @ stripped from recipe line, got %q", cmds[0].Run[0])
	}
	for _, c := range cmds {
		if c.Target == "internal-rule" {
			t.Error("a target not listed in .PHONY must not be extracted")
		}
	}
}

func TestExtractMakefileCommandsMissingFile(t *testing.T) {
	if _, err := ExtractMakefileCommands(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected an error for a missing Makefile, got nil")
	}
}

func TestExtractMakefileCommandsFromRealFile(t *testing.T) {
	// Exercises the actual Makefile, not a fixture, so a change to it that
	// breaks parsing fails this test immediately.
	cmds, err := ExtractMakefileCommands(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) == 0 {
		t.Fatal("expected at least one command from the real Makefile")
	}
	foundTest := false
	for _, c := range cmds {
		if c.Target == "test" {
			foundTest = true
			if len(c.Run) == 0 {
				t.Error("expected 'test' target to have at least one recipe line")
			}
		}
	}
	if !foundTest {
		t.Error("expected the real Makefile's 'test' target to be extracted")
	}
}

func TestRenderCommandsMarkdownIsMarkedGenerated(t *testing.T) {
	md := RenderCommandsMarkdown([]Command{{Target: "run", Run: []string{"go run ./cmd/api"}}}, "Makefile")
	if !strings.Contains(md, "GENERATED FILE") {
		t.Error("generated markdown must contain a do-not-edit marker")
	}
	if !strings.Contains(md, "make run") {
		t.Error("expected target name to appear in rendered markdown")
	}
}

func TestRenderCommandsMarkdownHandlesEmptyRecipe(t *testing.T) {
	md := RenderCommandsMarkdown([]Command{{Target: "noop"}}, "Makefile")
	if !strings.Contains(md, "no recipe lines found") {
		t.Error("expected an explicit note for a target with no recipe lines, not a silently empty code block")
	}
}
