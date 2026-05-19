package tools

import (
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
