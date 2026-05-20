package todo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestPersistentManagerWritesUniqueTodoFiles(t *testing.T) {
	workdir := t.TempDir()
	manager := NewPersistentManager(workdir)

	firstOut, err := manager.Update([]Item{{Content: "Plan persistence", Status: StatusInProgress}})
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
	if len(entries) != 2 {
		t.Fatalf("todo snapshot count = %d, want 2", len(entries))
	}
	if entries[0].Name() == entries[1].Name() {
		t.Fatalf("snapshot filenames must be unique, got %q twice", entries[0].Name())
	}

	var snapshots []snapshotFile
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(storeDir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) returned error: %v", entry.Name(), err)
		}
		var snapshot snapshotFile
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatalf("snapshot %s is not valid JSON: %v\n%s", entry.Name(), err, data)
		}
		if snapshot.CreatedAt == "" {
			t.Fatalf("snapshot %s missing created_at", entry.Name())
		}
		snapshots = append(snapshots, snapshot)
	}

	rendered := snapshots[0].Rendered + "\n" + snapshots[1].Rendered
	if !strings.Contains(rendered, firstOut) || !strings.Contains(rendered, secondOut) {
		t.Fatalf("snapshot rendered output missing updates:\n%s", rendered)
	}
	contents := snapshots[0].Items[0].Content + "\n" + snapshots[1].Items[0].Content
	if !strings.Contains(contents, "Plan persistence") || !strings.Contains(contents, "Verify files") {
		t.Fatalf("snapshot items missing todo contents:\n%s", contents)
	}
}
