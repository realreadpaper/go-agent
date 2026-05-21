package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/autonomous"
	"learn-claude-code-go/internal/background"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/protocols"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
	"learn-claude-code-go/internal/worktree"
)

type scriptedClient struct {
	responses []llm.Response
	calls     int
}

func (c *scriptedClient) Create(req llm.Request) (llm.Response, error) {
	resp := c.responses[c.calls]
	c.calls++
	return resp, nil
}

func TestAgentLoopSmokeRunsFakeToolCall(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{
			StopReason: "tool_use",
			Content: []llm.ContentBlock{{
				Type:  "tool_use",
				ID:    "tool-1",
				Name:  "echo",
				Input: map[string]any{"text": "hello"},
			}},
		},
		{
			StopReason: "end_turn",
			Content:    []llm.ContentBlock{{Type: "text", Text: "done"}},
		},
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec("echo", "Echo input text.", map[string]any{"type": "object"}),
		Handler: func(input map[string]any) (string, error) {
			return input["text"].(string), nil
		},
	})
	loop := &agent.Loop{Client: client, Model: "fake", Tools: reg, MaxRounds: 3}

	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: "call echo"}})
	if err != nil {
		t.Fatalf("loop.Run returned error: %v", err)
	}
	if resp.Content[0].Text != "done" || client.calls != 2 {
		t.Fatalf("resp=%+v calls=%d, want done after tool call", resp, client.calls)
	}
}

func TestStateManagersSmokeInTempDir(t *testing.T) {
	root := t.TempDir()
	taskManager := tasks.NewManager(root)
	task, err := taskManager.Create(tasks.CreateInput{Subject: "Smoke task"})
	if err != nil {
		t.Fatalf("Create task returned error: %v", err)
	}
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	tracker, err := protocols.NewTracker(root)
	if err != nil {
		t.Fatalf("protocols.NewTracker returned error: %v", err)
	}
	bg := background.NewManager(root)

	if _, err := bg.Run("printf smoke"); err != nil {
		t.Fatalf("background Run returned error: %v", err)
	}
	if err := teamManager.Send("lead", "alice", "hello"); err != nil {
		t.Fatalf("team Send returned error: %v", err)
	}
	inbox, err := teamManager.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox returned error: %v", err)
	}
	if len(inbox) != 1 || inbox[0].Content != "hello" {
		t.Fatalf("inbox = %+v, want hello", inbox)
	}

	req, err := tracker.Create(protocols.KindPlanApproval, "alice", "lead", map[string]any{"plan": "smoke"})
	if err != nil {
		t.Fatalf("Create request returned error: %v", err)
	}
	if _, err := tracker.Approve(req.ID, "ok"); err != nil {
		t.Fatalf("Approve returned error: %v", err)
	}

	controller := autonomous.NewController(teamManager, taskManager, autonomous.IdleConfig{
		PollInterval: time.Millisecond,
		Timeout:      time.Second,
	})
	result, err := controller.Wait(context.Background(), team.Teammate{Name: "alice", Role: "coder"})
	if err != nil {
		t.Fatalf("idle Wait returned error: %v", err)
	}
	if result.Action != autonomous.ActionWork || result.Task == nil || result.Task.ID != task.ID {
		t.Fatalf("idle result = %+v, want claimed task", result)
	}
	claimed, err := taskManager.Get(task.ID)
	if err != nil {
		t.Fatalf("Get task returned error: %v", err)
	}
	if claimed.Owner != "alice" || claimed.Status != tasks.StatusInProgress {
		t.Fatalf("claimed task = %+v, want alice in_progress", claimed)
	}
}

func TestWorktreeManagerSmokeInTempRepo(t *testing.T) {
	root := initGitRepo(t)
	taskManager := tasks.NewManager(root)
	task, err := taskManager.Create(tasks.CreateInput{Subject: "Isolated work"})
	if err != nil {
		t.Fatalf("Create task returned error: %v", err)
	}
	manager, err := worktree.NewManager(root, taskManager)
	if err != nil {
		t.Fatalf("worktree.NewManager returned error: %v", err)
	}
	wt, err := manager.Create("isolated-work", task.ID)
	if err != nil {
		t.Fatalf("Create worktree returned error: %v", err)
	}
	out, err := manager.Run(wt.Name, "pwd")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out, filepath.Join(root, ".worktrees", "isolated-work")) {
		t.Fatalf("pwd output = %q, want isolated worktree path", out)
	}
	if _, err := manager.Remove(wt.Name, true, true); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
}

func TestFullHarnessCommandSmokeCompilesAndReportsLocalCommands(t *testing.T) {
	root := t.TempDir()
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	taskManager := tasks.NewManager(root)
	if _, err := taskManager.Create(tasks.CreateInput{Subject: "Visible task"}); err != nil {
		t.Fatalf("Create task returned error: %v", err)
	}
	config, err := teamManager.Config()
	if err != nil {
		t.Fatalf("Config returned error: %v", err)
	}
	ready, err := taskManager.ListReady()
	if err != nil {
		t.Fatalf("ListReady returned error: %v", err)
	}
	if len(config.Members) != 0 || len(ready) != 1 || !strings.Contains(ready[0].Subject, "Visible") {
		t.Fatalf("config=%+v ready=%+v, want empty team and one ready task", config, ready)
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
