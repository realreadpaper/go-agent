package autonomous

import (
	"context"
	"strings"
	"testing"
	"time"

	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

func TestIdleClaimsReadyTaskAndSkipsBlockedTasks(t *testing.T) {
	root := t.TempDir()
	taskManager := tasks.NewManager(root)
	ready, err := taskManager.Create(tasks.CreateInput{Subject: "Implement parser"})
	if err != nil {
		t.Fatalf("Create ready returned error: %v", err)
	}
	blocker, err := taskManager.Create(tasks.CreateInput{Subject: "Design parser"})
	if err != nil {
		t.Fatalf("Create blocker returned error: %v", err)
	}
	if _, err := taskManager.Create(tasks.CreateInput{Subject: "Blocked parser", BlockedBy: []int{blocker.ID}}); err != nil {
		t.Fatalf("Create blocked returned error: %v", err)
	}
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	controller := NewController(teamManager, taskManager, IdleConfig{PollInterval: time.Millisecond, Timeout: time.Second})

	result, err := controller.Wait(context.Background(), team.Teammate{Name: "alice", Role: "coder"})
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if result.Action != ActionWork || result.Task.ID != ready.ID || !strings.Contains(result.Message, "<auto-claimed>") {
		t.Fatalf("result = %+v, want auto-claimed ready task", result)
	}
	claimed, err := taskManager.Get(ready.ID)
	if err != nil {
		t.Fatalf("Get ready returned error: %v", err)
	}
	if claimed.Owner != "alice" || claimed.Status != tasks.StatusInProgress {
		t.Fatalf("claimed task = %+v, want alice in_progress", claimed)
	}
	blocked, err := taskManager.Get(3)
	if err != nil {
		t.Fatalf("Get blocked returned error: %v", err)
	}
	if blocked.Owner != "" || blocked.Status != tasks.StatusPending {
		t.Fatalf("blocked task = %+v, want untouched", blocked)
	}
}

func TestIdleReturnsInboxBeforeTaskClaim(t *testing.T) {
	root := t.TempDir()
	taskManager := tasks.NewManager(root)
	if _, err := taskManager.Create(tasks.CreateInput{Subject: "Ready work"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	if err := teamManager.Send("lead", "alice", "please pause"); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	controller := NewController(teamManager, taskManager, IdleConfig{PollInterval: time.Millisecond, Timeout: time.Second})

	result, err := controller.Wait(context.Background(), team.Teammate{Name: "alice", Role: "coder"})
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if result.Action != ActionWork || !strings.Contains(result.Message, "<inbox>") || strings.Contains(result.Message, "<auto-claimed>") {
		t.Fatalf("result = %+v, want inbox wakeup before task claim", result)
	}
}

func TestIdleTimeoutShutsDown(t *testing.T) {
	root := t.TempDir()
	taskManager := tasks.NewManager(root)
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	controller := NewController(teamManager, taskManager, IdleConfig{PollInterval: time.Millisecond, Timeout: 5 * time.Millisecond})

	result, err := controller.Wait(context.Background(), team.Teammate{Name: "alice", Role: "coder"})
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if result.Action != ActionShutdown {
		t.Fatalf("result action = %q, want shutdown", result.Action)
	}
}

func TestIdentityHookReinjectsShortContext(t *testing.T) {
	hook := IdentityHook(team.Teammate{Name: "alice", Role: "coder"}, "default")
	messages := []llm.Message{{Role: "user", Content: "continue"}}
	if err := hook(&messages); err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if len(messages) != 2 || !strings.Contains(messages[0].Content.(string), "<identity>") {
		t.Fatalf("messages after hook = %+v, want identity inserted", messages)
	}
}

func TestRegisterAutonomousTools(t *testing.T) {
	root := t.TempDir()
	taskManager := tasks.NewManager(root)
	task, err := taskManager.Create(tasks.CreateInput{Subject: "Ready work"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	reg := tools.NewRegistry()
	Register(reg, NewController(teamManager, taskManager, IdleConfig{PollInterval: time.Millisecond, Timeout: time.Second}), team.Teammate{Name: "alice", Role: "coder"})

	out := reg.Run("claim_task", map[string]any{"task_id": task.ID})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"owner": "alice"`) {
		t.Fatalf("claim_task output = %q", out)
	}
}
