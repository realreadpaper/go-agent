package main

import (
	"strings"
	"testing"
)

func TestSystemPromptTellsModelToReportLoadedSkillPath(t *testing.T) {
	prompt := systemPrompt("/workspace", "- git-commit: Commit safely")

	for _, want := range []string{
		"Skills available:",
		"- git-commit: Commit safely",
		"include the <path> value",
		"call create_skill",
		"call update_skill",
		"call load_skill with the created skill name",
		"call load_skill with the updated skill name",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("systemPrompt missing %q:\n%s", want, prompt)
		}
	}
}
