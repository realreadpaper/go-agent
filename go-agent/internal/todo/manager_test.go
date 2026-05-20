package todo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"learn-claude-code-go/internal/tools"
)

func TestManagerUpdateValidatesAndRendersTodos(t *testing.T) {
	manager := NewManager()

	out, err := manager.Update([]Item{
		{Content: "Design plan", Status: "pending", ActiveForm: "Designing plan"},
		{Content: "Write code", Status: "in_progress", ActiveForm: "Writing code"},
		{Content: "Run tests", Status: "completed", ActiveForm: "Running tests"},
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	for _, want := range []string{
		"[ ] Design plan",
		"[>] Write code",
		"[x] Run tests",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered todo list missing %q:\n%s", want, out)
		}
	}
}

func TestManagerRejectsMultipleInProgressItems(t *testing.T) {
	manager := NewManager()

	_, err := manager.Update([]Item{
		{Content: "One", Status: "in_progress"},
		{Content: "Two", Status: "in_progress"},
	})
	if err == nil || !strings.Contains(err.Error(), "only one todo can be in_progress") {
		t.Fatalf("error = %v, want multiple in_progress error", err)
	}
}

func TestManagerRejectsInvalidStatusAndEmptyContent(t *testing.T) {
	manager := NewManager()

	_, err := manager.Update([]Item{{Content: "Bad", Status: "blocked"}})
	if err == nil || !strings.Contains(err.Error(), "invalid todo status") {
		t.Fatalf("invalid status error = %v", err)
	}

	_, err = manager.Update([]Item{{Content: " ", Status: "pending"}})
	if err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("empty content error = %v", err)
	}
}

func TestManagerRejectsTooManyItems(t *testing.T) {
	manager := NewManager()
	items := make([]Item, 21)
	for i := range items {
		items[i] = Item{Content: "Task", Status: "pending"}
	}

	_, err := manager.Update(items)
	if err == nil || !strings.Contains(err.Error(), "at most 20 todos") {
		t.Fatalf("too many items error = %v", err)
	}
}

func TestRegisterToolUpdatesManagerFromToolInput(t *testing.T) {
	manager := NewManager()
	reg := tools.NewRegistry()
	Register(reg, manager)

	out := reg.Run("todo", map[string]any{
		"items": []any{
			map[string]any{"content": "Plan", "status": "pending", "activeForm": "Planning"},
			map[string]any{"content": "Code", "status": "in_progress", "activeForm": "Coding"},
		},
	})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("todo tool returned error: %q", out)
	}
	if !strings.Contains(out, "[ ] Plan") || !strings.Contains(out, "[>] Code") {
		t.Fatalf("todo tool output = %q", out)
	}
}

func TestPersistentManagerKeepsOnlyFinalTodoFileForOneRun(t *testing.T) {
	workdir := t.TempDir()
	manager := NewPersistentManager(workdir)

	_, err := manager.Update([]Item{{Content: "Plan persistence", Status: StatusInProgress}})
	if err != nil {
		t.Fatalf("first Update returned error: %v", err)
	}
	secondOut, err := manager.Update([]Item{{Content: "Verify files", Status: StatusCompleted}})
	if err != nil {
		t.Fatalf("second Update returned error: %v", err)
	}

	storeDir := filepath.Join(workdir, ".goagent", "todo")
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) returned error: %v", storeDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("todo snapshot count = %d, want 1 final file", len(entries))
	}

	data, err := os.ReadFile(filepath.Join(storeDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", entries[0].Name(), err)
	}
	var snapshot snapshotFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("snapshot %s is not valid JSON: %v\n%s", entries[0].Name(), err, data)
	}
	if snapshot.CreatedAt == "" {
		t.Fatalf("snapshot %s missing created_at", entries[0].Name())
	}
	if snapshot.Rendered != secondOut {
		t.Fatalf("rendered snapshot = %q, want final output %q", snapshot.Rendered, secondOut)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].Content != "Verify files" {
		t.Fatalf("snapshot items = %+v, want final todo only", snapshot.Items)
	}
}

func TestFormatSnapshotTimeUsesReadableLocalStyle(t *testing.T) {
	ts := time.Date(2026, 5, 20, 10, 53, 11, 86_022_000, time.FixedZone("CST", 8*60*60))

	got := formatSnapshotTime(ts)

	want := "2026-05-20 10:53:11 +08:00"
	if got != want {
		t.Fatalf("formatSnapshotTime() = %q, want %q", got, want)
	}
}

func TestFormatSnapshotFileNameUsesReadableLocalStyle(t *testing.T) {
	ts := time.Date(2026, 5, 20, 10, 59, 28, 46_967_000, time.FixedZone("CST", 8*60*60))

	got := formatSnapshotFileName(ts)

	want := "todo-2026-05-20-10-59-28-046967.json"
	if got != want {
		t.Fatalf("formatSnapshotFileName() = %q, want %q", got, want)
	}
}
