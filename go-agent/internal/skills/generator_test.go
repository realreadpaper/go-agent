package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"learn-claude-code-go/internal/tools"
)

func TestCreateSkillWritesFrontmatterSkillFile(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}

	result, err := loader.Create(SkillDraft{
		Name:        "Git Commit Safety",
		Description: "Use when preparing git commits or checking staged changes",
		Content:     "# Git Commit Safety\n\n## Overview\nCheck status before staging files.",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	path := filepath.Join(root, "git-commit-safety", "SKILL.md")
	if result.Name != "git-commit-safety" || result.Path != path {
		t.Fatalf("Create result = %+v, want normalized name and path %s", result, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"---\nname: git-commit-safety\n",
		"description: Use when preparing git commits or checking staged changes\n---",
		"# Git Commit Safety",
		"Check status before staging files.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated SKILL.md missing %q:\n%s", want, text)
		}
	}

	loaded, err := loader.Load("git-commit-safety")
	if err != nil {
		t.Fatalf("Load newly created skill returned error: %v", err)
	}
	if !strings.Contains(loaded, "Check status before staging files.") {
		t.Fatalf("created skill was not added to loader index:\n%s", loaded)
	}
}

func TestCreateSkillRejectsExistingSkillUnlessOverwrite(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}
	draft := SkillDraft{
		Name:        "repeatable-check",
		Description: "Use when checking repeatable tasks",
		Content:     "# Repeatable Check\n\nOriginal content.",
	}
	if _, err := loader.Create(draft); err != nil {
		t.Fatalf("initial Create returned error: %v", err)
	}
	if _, err := loader.Create(draft); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second Create error = %v, want already exists", err)
	}

	draft.Overwrite = true
	draft.Content = "# Repeatable Check\n\nUpdated content."
	if _, err := loader.Create(draft); err != nil {
		t.Fatalf("overwrite Create returned error: %v", err)
	}
	loaded, err := loader.Load("repeatable-check")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !strings.Contains(loaded, "Updated content.") {
		t.Fatalf("overwrite did not update loader index:\n%s", loaded)
	}
}

func TestUpdateSkillRequiresExistingSkill(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}
	draft := SkillDraft{
		Name:        "repeatable-check",
		Description: "Use when checking repeatable tasks",
		Content:     "# Repeatable Check\n\nOriginal content.",
	}
	if _, err := loader.Update(draft); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Update missing skill error = %v, want does not exist", err)
	}
	if _, err := loader.Create(draft); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	draft.Content = "# Repeatable Check\n\nUpdated with a clearer checklist."
	result, err := loader.Update(draft)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if result.Name != "repeatable-check" {
		t.Fatalf("Update result name = %q, want repeatable-check", result.Name)
	}
	loaded, err := loader.Load("repeatable-check")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !strings.Contains(loaded, "Updated with a clearer checklist.") {
		t.Fatalf("updated skill was not reloaded:\n%s", loaded)
	}
}

func TestCreateSkillValidatesDraft(t *testing.T) {
	loader, err := NewLoader(t.TempDir())
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}

	cases := []SkillDraft{
		{Name: "../bad", Description: "Use when paths escape", Content: "# Bad"},
		{Name: "valid-name", Description: "", Content: "# Bad"},
		{Name: "valid-name", Description: "Use when testing", Content: ""},
	}
	for _, draft := range cases {
		if _, err := loader.Create(draft); err == nil {
			t.Fatalf("Create(%+v) returned nil error, want validation error", draft)
		}
	}
}

func TestRegisterCreateSkillTool(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}
	reg := tools.NewRegistry()
	RegisterCreateSkill(reg, loader)

	out := reg.Run("create_skill", map[string]any{
		"name":        "demo repeatable check",
		"description": "Use when checking git status before commits",
		"content":     "# Demo Repeatable Check\n\nRun git status first.",
	})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("create_skill returned error: %q", out)
	}
	if !strings.Contains(out, "demo-repeatable-check") || !strings.Contains(out, "SKILL.md") {
		t.Fatalf("create_skill output = %q", out)
	}
}

func TestRegisterUpdateSkillTool(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root)
	if err != nil {
		t.Fatalf("NewLoader returned error: %v", err)
	}
	if _, err := loader.Create(SkillDraft{
		Name:        "demo-repeatable-check",
		Description: "Use when checking git status before commits",
		Content:     "# Demo Repeatable Check\n\nRun git status first.",
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	reg := tools.NewRegistry()
	RegisterUpdateSkill(reg, loader)

	out := reg.Run("update_skill", map[string]any{
		"name":        "demo repeatable check",
		"description": "Use when improving git status checks before commits",
		"content":     "# Demo Repeatable Check\n\nRun git status and inspect diffs.",
	})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("update_skill returned error: %q", out)
	}
	if !strings.Contains(out, "demo-repeatable-check") || !strings.Contains(out, "SKILL.md") {
		t.Fatalf("update_skill output = %q", out)
	}
}
