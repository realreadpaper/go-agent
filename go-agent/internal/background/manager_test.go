package background

import (
	"strings"
	"testing"
	"time"

	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

func TestRunReturnsTaskIDAndCheckEventuallyCompletes(t *testing.T) {
	manager := NewManager(t.TempDir())

	task, err := manager.Run("echo done")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if task.ID == "" {
		t.Fatalf("task id is empty")
	}

	status := waitForStatus(t, manager, task.ID, StatusCompleted)
	if !strings.Contains(status.Output, "done") {
		t.Fatalf("completed output = %q, want done", status.Output)
	}
}

func TestDrainNotificationsReturnsCompletedResultOnce(t *testing.T) {
	manager := NewManager(t.TempDir())
	task, err := manager.Run("echo done")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	waitForStatus(t, manager, task.ID, StatusCompleted)

	notifications := waitForNotifications(t, manager)
	if len(notifications) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifications))
	}
	if notifications[0].TaskID != task.ID || !strings.Contains(notifications[0].Summary, "done") {
		t.Fatalf("notification = %+v", notifications[0])
	}
	if got := manager.DrainNotifications(); len(got) != 0 {
		t.Fatalf("DrainNotifications second call = %+v, want empty", got)
	}
}

func TestBeforeCallInjectsBackgroundResults(t *testing.T) {
	manager := NewManager(t.TempDir())
	task, err := manager.Run("echo done")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	waitForStatus(t, manager, task.ID, StatusCompleted)

	messages := []llm.Message{{Role: "user", Content: "continue"}}
	waitForInjectedBackgroundResults(t, manager, &messages)
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	injected, ok := messages[1].Content.(string)
	if !ok {
		t.Fatalf("injected content type = %T", messages[1].Content)
	}
	for _, want := range []string{"<background-results>", task.ID, "done", "</background-results>"} {
		if !strings.Contains(injected, want) {
			t.Fatalf("injected message missing %q:\n%s", want, injected)
		}
	}
}

func TestRegisterBackgroundTools(t *testing.T) {
	manager := NewManager(t.TempDir())
	reg := tools.NewRegistry()
	Register(reg, manager)

	out := reg.Run("background_run", map[string]any{"command": "echo done"})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"id"`) || !strings.Contains(out, `"running"`) {
		t.Fatalf("background_run output = %q", out)
	}
	taskID := extractTaskID(t, out)

	waitForStatus(t, manager, taskID, StatusCompleted)
	out = reg.Run("background_check", map[string]any{"task_id": taskID})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"completed"`) || !strings.Contains(out, "done") {
		t.Fatalf("background_check output = %q", out)
	}
}

func waitForStatus(t *testing.T, manager *Manager, taskID, want string) Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := manager.Check(taskID)
		if err != nil {
			t.Fatalf("Check returned error: %v", err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, _ := manager.Check(taskID)
	t.Fatalf("task %s status = %s, want %s", taskID, task.Status, want)
	return Task{}
}

func waitForNotifications(t *testing.T, manager *Manager) []Notification {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		notifications := manager.DrainNotifications()
		if len(notifications) > 0 {
			return notifications
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for notifications")
	return nil
}

func waitForInjectedBackgroundResults(t *testing.T, manager *Manager, messages *[]llm.Message) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := manager.BeforeCall(messages); err != nil {
			t.Fatalf("BeforeCall returned error: %v", err)
		}
		if len(*messages) > 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for BeforeCall background injection")
}

func extractTaskID(t *testing.T, text string) string {
	t.Helper()
	start := strings.Index(text, `"id": "`)
	if start < 0 {
		t.Fatalf("id not found in %q", text)
	}
	rest := text[start+len(`"id": "`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("id terminator not found in %q", text)
	}
	return rest[:end]
}
