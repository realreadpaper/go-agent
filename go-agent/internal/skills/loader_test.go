package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"learn-claude-code-go/internal/tools"
)

func TestLoaderScansSkillFilesAndUsesFrontmatterName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha/SKILL.md", `---
name: custom-alpha
description: Alpha workflow
---

# Alpha

Detailed alpha instructions.
`)
	writeSkill(t, root, "nested/beta/SKILL.md", `---
description: Beta workflow
---

# Beta

Detailed beta instructions.
`)

	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}

	descriptions := loader.Descriptions()
	for _, want := range []string{
		"- custom-alpha: Alpha workflow",
		"- beta: Beta workflow",
	} {
		if !strings.Contains(descriptions, want) {
			t.Fatalf("Descriptions() missing %q:\n%s", want, descriptions)
		}
	}
	if strings.Contains(descriptions, "Detailed alpha instructions") {
		t.Fatalf("Descriptions() should not include full skill body:\n%s", descriptions)
	}
}

func TestLoaderLoadsFullSkillContent(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "code-review/SKILL.md", `---
name: code-review
description: Review code changes
---

# Code Review

Check bugs before style.
`)

	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}

	loaded, err := loader.Load("code-review")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for _, want := range []string{
		`<skill name="code-review">`,
		"<path>",
		filepath.Join("code-review", "SKILL.md"),
		"# Code Review",
		"Check bugs before style.",
		"</skill>",
	} {
		if !strings.Contains(loaded, want) {
			t.Fatalf("Load() missing %q:\n%s", want, loaded)
		}
	}
}

func TestLoaderReportsUnknownSkill(t *testing.T) {
	loader, err := NewLoader(t.TempDir())
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}

	_, err = loader.Load("missing")
	if err == nil || !strings.Contains(err.Error(), "unknown skill: missing") {
		t.Fatalf("Load missing error = %v, want unknown skill error", err)
	}
}

func TestRegisterLoadSkillTool(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "git-commit/SKILL.md", `---
name: git-commit
description: Commit safely
---

# Git Commit

Only stage related files.
`)
	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}
	reg := tools.NewRegistry()
	RegisterLoadSkill(reg, loader)

	out := reg.Run("load_skill", map[string]any{"name": "git-commit"})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("load_skill returned error: %q", out)
	}
	if !strings.Contains(out, "Only stage related files.") {
		t.Fatalf("load_skill output = %q", out)
	}
}

func writeSkill(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
