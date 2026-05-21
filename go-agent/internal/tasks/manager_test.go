package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"learn-claude-code-go/internal/tools"
)

func TestManagerCreatePersistsIncrementingTasks(t *testing.T) {
	manager := NewManager(t.TempDir())

	first, err := manager.Create(CreateInput{Subject: "Design API", Description: "Define endpoints"})
	if err != nil {
		t.Fatalf("Create first returned error: %v", err)
	}
	second, err := manager.Create(CreateInput{Subject: "Implement API", Description: "Write handlers", BlockedBy: []int{first.ID}})
	if err != nil {
		t.Fatalf("Create second returned error: %v", err)
	}

	if first.ID != 1 || second.ID != 2 {
		t.Fatalf("ids = %d, %d; want 1, 2", first.ID, second.ID)
	}
	if second.Status != StatusPending || second.BlockedBy[0] != first.ID {
		t.Fatalf("second task = %+v, want pending blocked by first", second)
	}

	path := filepath.Join(manager.Dir(), "task_2.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", path, err)
	}
	var persisted Task
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("task file is not JSON: %v\n%s", err, data)
	}
	if persisted.ID != 2 || persisted.Subject != "Implement API" {
		t.Fatalf("persisted task = %+v", persisted)
	}
}

func TestManagerCompletingTaskUnblocksDependents(t *testing.T) {
	manager := NewManager(t.TempDir())
	design, err := manager.Create(CreateInput{Subject: "Design API"})
	if err != nil {
		t.Fatalf("Create design returned error: %v", err)
	}
	implement, err := manager.Create(CreateInput{Subject: "Implement API", BlockedBy: []int{design.ID}})
	if err != nil {
		t.Fatalf("Create implement returned error: %v", err)
	}

	if _, err := manager.Update(UpdateInput{ID: design.ID, Status: StatusCompleted}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	updated, err := manager.Get(implement.ID)
	if err != nil {
		t.Fatalf("Get implement returned error: %v", err)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("blockedBy after completion = %v, want empty", updated.BlockedBy)
	}
}

func TestManagerListReadyFiltersBlockedOwnedAndNonPendingTasks(t *testing.T) {
	manager := NewManager(t.TempDir())
	ready, err := manager.Create(CreateInput{Subject: "Ready"})
	if err != nil {
		t.Fatalf("Create ready returned error: %v", err)
	}
	blocker, err := manager.Create(CreateInput{Subject: "Blocker"})
	if err != nil {
		t.Fatalf("Create blocker returned error: %v", err)
	}
	if _, err := manager.Create(CreateInput{Subject: "Blocked", BlockedBy: []int{blocker.ID}}); err != nil {
		t.Fatalf("Create blocked returned error: %v", err)
	}
	owned, err := manager.Create(CreateInput{Subject: "Owned"})
	if err != nil {
		t.Fatalf("Create owned returned error: %v", err)
	}
	if _, err := manager.Update(UpdateInput{ID: owned.ID, Owner: "agent-a"}); err != nil {
		t.Fatalf("Update owned returned error: %v", err)
	}
	done, err := manager.Create(CreateInput{Subject: "Done"})
	if err != nil {
		t.Fatalf("Create done returned error: %v", err)
	}
	if _, err := manager.Update(UpdateInput{ID: done.ID, Status: StatusCompleted}); err != nil {
		t.Fatalf("Update done returned error: %v", err)
	}

	got, err := manager.ListReady()
	if err != nil {
		t.Fatalf("ListReady returned error: %v", err)
	}

	if len(got) != 2 || got[0].ID != ready.ID || got[1].ID != blocker.ID {
		t.Fatalf("ready tasks = %+v, want ready and blocker only", got)
	}
}

func TestManagerClaimReadyTaskAssignsOwnerAndSkipsBlocked(t *testing.T) {
	manager := NewManager(t.TempDir())
	ready, err := manager.Create(CreateInput{Subject: "Ready"})
	if err != nil {
		t.Fatalf("Create ready returned error: %v", err)
	}
	blocker, err := manager.Create(CreateInput{Subject: "Blocker"})
	if err != nil {
		t.Fatalf("Create blocker returned error: %v", err)
	}
	if _, err := manager.Create(CreateInput{Subject: "Blocked", BlockedBy: []int{blocker.ID}}); err != nil {
		t.Fatalf("Create blocked returned error: %v", err)
	}

	claimed, ok, err := manager.ClaimReady("alice")
	if err != nil {
		t.Fatalf("ClaimReady returned error: %v", err)
	}
	if !ok {
		t.Fatal("ClaimReady ok = false, want true")
	}
	if claimed.ID != ready.ID || claimed.Owner != "alice" || claimed.Status != StatusInProgress {
		t.Fatalf("claimed = %+v, want ready owned by alice and in_progress", claimed)
	}
	blocked, err := manager.Get(3)
	if err != nil {
		t.Fatalf("Get blocked returned error: %v", err)
	}
	if blocked.Owner != "" || blocked.Status != StatusPending {
		t.Fatalf("blocked task = %+v, want untouched pending task", blocked)
	}
}

func TestManagerRejectsInvalidStatusAndRestoresFromDisk(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	task, err := manager.Create(CreateInput{Subject: "Persist me"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := manager.Update(UpdateInput{ID: task.ID, Status: "blocked"}); err == nil || !strings.Contains(err.Error(), "invalid task status") {
		t.Fatalf("invalid status error = %v, want invalid task status", err)
	}

	restored, err := LoadManager(root)
	if err != nil {
		t.Fatalf("LoadManager returned error: %v", err)
	}
	got, err := restored.Get(task.ID)
	if err != nil {
		t.Fatalf("Get restored task returned error: %v", err)
	}
	if got.Subject != "Persist me" {
		t.Fatalf("restored task = %+v", got)
	}
}

func TestManagerUsesStateMachineForTransitions(t *testing.T) {
	manager := NewManager(t.TempDir())
	task, err := manager.Create(CreateInput{Subject: "Stateful task"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := manager.Update(UpdateInput{ID: task.ID, Status: StatusInProgress}); err != nil {
		t.Fatalf("pending -> in_progress returned error: %v", err)
	}
	if _, err := manager.Update(UpdateInput{ID: task.ID, Status: StatusInProgress}); err != nil {
		t.Fatalf("same-state transition should be idempotent: %v", err)
	}
	if _, err := manager.Update(UpdateInput{ID: task.ID, Status: StatusCompleted}); err != nil {
		t.Fatalf("in_progress -> completed returned error: %v", err)
	}

	_, err = manager.Update(UpdateInput{ID: task.ID, Status: StatusPending})
	if err == nil || !strings.Contains(err.Error(), "invalid task transition completed -> pending") {
		t.Fatalf("completed -> pending error = %v, want invalid transition", err)
	}
	got, err := manager.Get(task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("status after rejected transition = %q, want completed", got.Status)
	}
}

func TestRegisterTaskTools(t *testing.T) {
	manager := NewManager(t.TempDir())
	reg := tools.NewRegistry()
	Register(reg, manager)

	out := reg.Run("task_create", map[string]any{
		"subject":     "Design API",
		"description": "Define endpoints",
	})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"id": 1`) {
		t.Fatalf("task_create output = %q", out)
	}

	out = reg.Run("task_update", map[string]any{
		"task_id": 1,
		"status":  StatusInProgress,
		"owner":   "agent-a",
	})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"owner": "agent-a"`) {
		t.Fatalf("task_update output = %q", out)
	}

	out = reg.Run("task_get", map[string]any{"task_id": 1})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"status": "in_progress"`) {
		t.Fatalf("task_get output = %q", out)
	}

	out = reg.Run("task_list", map[string]any{})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"tasks"`) || !strings.Contains(out, `"ready"`) {
		t.Fatalf("task_list output = %q", out)
	}
}
