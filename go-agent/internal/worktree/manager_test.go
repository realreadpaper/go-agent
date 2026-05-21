package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/tools"
)

func TestManagerCreateRunKeepAndRemoveWorktree(t *testing.T) {
	root := initGitRepo(t)
	taskManager := tasks.NewManager(root)
	task, err := taskManager.Create(tasks.CreateInput{Subject: "Auth refactor"})
	if err != nil {
		t.Fatalf("Create task returned error: %v", err)
	}
	manager, err := NewManager(root, taskManager)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	wt, err := manager.Create("auth-refactor", task.ID)
	if err != nil {
		t.Fatalf("Create worktree returned error: %v", err)
	}
	if wt.Name != "auth-refactor" || wt.Branch != "wt/auth-refactor" || wt.Status != StatusActive {
		t.Fatalf("worktree = %+v, want auth-refactor active branch", wt)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees", "auth-refactor", "README.md")); err != nil {
		t.Fatalf("worktree README not created: %v", err)
	}
	updatedTask, err := taskManager.Get(task.ID)
	if err != nil {
		t.Fatalf("Get task returned error: %v", err)
	}
	if updatedTask.Worktree != wt.Path || updatedTask.Status != tasks.StatusInProgress {
		t.Fatalf("task after create = %+v, want bound worktree and in_progress", updatedTask)
	}

	out, err := manager.Run("auth-refactor", "pwd && git status --short")
	if err != nil {
		t.Fatalf("Run returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join(root, ".worktrees", "auth-refactor")) {
		t.Fatalf("Run output = %q, want worktree cwd", out)
	}

	kept, err := manager.Keep("auth-refactor")
	if err != nil {
		t.Fatalf("Keep returned error: %v", err)
	}
	if kept.Status != StatusKept {
		t.Fatalf("kept status = %q, want kept", kept.Status)
	}

	removed, err := manager.Remove("auth-refactor", true, true)
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if removed.Status != StatusRemoved {
		t.Fatalf("removed status = %q, want removed", removed.Status)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees", "auth-refactor")); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still exists or stat error = %v", err)
	}
	completed, err := taskManager.Get(task.ID)
	if err != nil {
		t.Fatalf("Get completed task returned error: %v", err)
	}
	if completed.Status != tasks.StatusCompleted || completed.Worktree != "" {
		t.Fatalf("task after remove = %+v, want completed with cleared worktree", completed)
	}

	events, err := manager.Events(20)
	if err != nil {
		t.Fatalf("Events returned error: %v", err)
	}
	joined := eventsText(events)
	for _, want := range []string{"worktree.create.before", "worktree.create.after", "worktree.keep", "worktree.remove.before", "worktree.remove.after", "task.completed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("events = %s, want %s", joined, want)
		}
	}
}

func TestRegisterWorktreeTools(t *testing.T) {
	root := initGitRepo(t)
	taskManager := tasks.NewManager(root)
	task, err := taskManager.Create(tasks.CreateInput{Subject: "Tool task"})
	if err != nil {
		t.Fatalf("Create task returned error: %v", err)
	}
	manager, err := NewManager(root, taskManager)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	reg := tools.NewRegistry()
	Register(reg, manager)

	out := reg.Run("worktree_create", map[string]any{"name": "tool-task", "task_id": task.ID})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"name": "tool-task"`) {
		t.Fatalf("worktree_create output = %q", out)
	}
	out = reg.Run("worktree_run", map[string]any{"name": "tool-task", "command": "pwd"})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, ".worktrees/tool-task") {
		t.Fatalf("worktree_run output = %q", out)
	}
	out = reg.Run("worktree_list", map[string]any{})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"worktrees"`) {
		t.Fatalf("worktree_list output = %q", out)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README returned error: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")
	return root
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func eventsText(events []Event) string {
	var b strings.Builder
	for _, event := range events {
		b.WriteString(event.Type)
		b.WriteByte('\n')
	}
	return b.String()
}
