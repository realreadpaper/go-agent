package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryRunsRegisteredToolAndFormatsErrors(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Spec: Spec("echo", "Echo input", map[string]any{"type": "object"}),
		Handler: func(input map[string]any) (string, error) {
			return input["text"].(string), nil
		},
	})

	if got := reg.Run("echo", map[string]any{"text": "hello"}); got != "hello" {
		t.Fatalf("Run echo = %q, want hello", got)
	}
	if got := reg.Run("missing", nil); !strings.Contains(got, "unknown tool") {
		t.Fatalf("Run missing = %q", got)
	}
}

func TestRegisterBashRunsCommandInWorkdir(t *testing.T) {
	workdir := t.TempDir()
	reg := NewRegistry()
	RegisterBash(reg, workdir)

	out := reg.Run("bash", map[string]any{"command": "pwd && printf ok > result.txt && cat result.txt"})
	if !strings.Contains(out, workdir) {
		t.Fatalf("bash output %q does not include workdir %q", out, workdir)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("bash output %q does not include command output", out)
	}
}

func TestRegisterBashRejectsDangerousCommands(t *testing.T) {
	reg := NewRegistry()
	RegisterBash(reg, t.TempDir())

	out := reg.Run("bash", map[string]any{"command": "sudo shutdown now"})
	if !strings.Contains(out, "dangerous command") {
		t.Fatalf("dangerous command output = %q", out)
	}
}

func TestRegisterBashRequiresCommandString(t *testing.T) {
	reg := NewRegistry()
	RegisterBash(reg, t.TempDir())

	out := reg.Run("bash", map[string]any{"command": 42})
	if !strings.Contains(out, "command must be a string") {
		t.Fatalf("invalid command output = %q", out)
	}
}

func TestFileToolsReadFileHonorsWorkspaceAndLimit(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "notes.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	reg := NewRegistry()
	RegisterFileTools(reg, workdir)

	out := reg.Run("read_file", map[string]any{"path": "notes.txt", "limit": 2})
	if out != "one\ntwo" {
		t.Fatalf("read_file output = %q, want first two lines", out)
	}
}

func TestFileToolsRejectPathEscapingWorkspace(t *testing.T) {
	workdir := t.TempDir()
	reg := NewRegistry()
	RegisterFileTools(reg, workdir)

	out := reg.Run("read_file", map[string]any{"path": "../outside.txt"})
	if !strings.Contains(out, "path escapes workspace") {
		t.Fatalf("read_file escape output = %q", out)
	}
}

func TestFileToolsWriteFileCreatesParentDirectories(t *testing.T) {
	workdir := t.TempDir()
	reg := NewRegistry()
	RegisterFileTools(reg, workdir)

	out := reg.Run("write_file", map[string]any{
		"path":    "nested/greet.txt",
		"content": "hello",
	})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("write_file returned error: %q", out)
	}
	got, err := os.ReadFile(filepath.Join(workdir, "nested", "greet.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("written file = %q, want hello", string(got))
	}
}

func TestFileToolsEditFileReplacesOnlyFirstMatch(t *testing.T) {
	workdir := t.TempDir()
	path := filepath.Join(workdir, "repeat.txt")
	if err := os.WriteFile(path, []byte("old\nold\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	reg := NewRegistry()
	RegisterFileTools(reg, workdir)

	out := reg.Run("edit_file", map[string]any{
		"path":     "repeat.txt",
		"old_text": "old",
		"new_text": "new",
	})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("edit_file returned error: %q", out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(got) != "new\nold\n" {
		t.Fatalf("edited file = %q, want first match replaced", string(got))
	}
}

func TestFileToolsEditFileReportsMissingText(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	reg := NewRegistry()
	RegisterFileTools(reg, workdir)

	out := reg.Run("edit_file", map[string]any{
		"path":     "note.txt",
		"old_text": "missing",
		"new_text": "new",
	})
	if !strings.Contains(out, "text not found in note.txt") {
		t.Fatalf("missing text output = %q", out)
	}
}
