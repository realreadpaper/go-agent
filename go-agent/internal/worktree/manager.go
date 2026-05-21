package worktree

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/tools"
)

const (
	StatusActive  = "active"
	StatusKept    = "kept"
	StatusRemoved = "removed"

	commandTimeout = 180 * time.Second
)

type Worktree struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	TaskID int    `json:"task_id"`
	Status string `json:"status"`
}

type Event struct {
	Type      string         `json:"type"`
	Name      string         `json:"name,omitempty"`
	TaskID    int            `json:"task_id,omitempty"`
	Path      string         `json:"path,omitempty"`
	Branch    string         `json:"branch,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt string         `json:"created_at"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type indexFile struct {
	Worktrees []Worktree `json:"worktrees"`
}

// Manager 是 git worktree 的控制面。
// 它把 git worktree 命令、.worktrees/index.json、events.jsonl 和 task graph 绑在一起。
type Manager struct {
	mu      sync.Mutex
	root    string
	baseDir string
	index   string
	events  string
	tasks   *tasks.Manager
	items   map[string]Worktree
}

func NewManager(root string, taskManager *tasks.Manager) (*Manager, error) {
	manager := &Manager{
		root:    root,
		baseDir: filepath.Join(root, ".worktrees"),
		index:   filepath.Join(root, ".worktrees", "index.json"),
		events:  filepath.Join(root, ".worktrees", "events.jsonl"),
		tasks:   taskManager,
		items:   map[string]Worktree{},
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Create(name string, taskID int) (Worktree, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Worktree{}, err
	}
	if taskID <= 0 {
		return Worktree{}, fmt.Errorf("task_id is required")
	}
	path := filepath.Join(m.baseDir, name)
	branch := "wt/" + name
	wt := Worktree{Name: name, Path: path, Branch: branch, TaskID: taskID, Status: StatusActive}

	m.mu.Lock()
	if _, exists := m.items[name]; exists {
		m.mu.Unlock()
		return Worktree{}, fmt.Errorf("worktree already exists: %s", name)
	}
	m.mu.Unlock()

	_ = m.appendEvent("worktree.create.before", wt, nil, nil)
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		_ = m.appendEvent("worktree.create.failed", wt, err, nil)
		return Worktree{}, err
	}
	if out, err := m.git("worktree", "add", "-b", branch, path, "HEAD"); err != nil {
		_ = m.appendEvent("worktree.create.failed", wt, err, map[string]any{"output": out})
		return Worktree{}, fmt.Errorf("git worktree add failed: %w\n%s", err, out)
	}
	if m.tasks != nil {
		if _, err := m.tasks.Update(tasks.UpdateInput{ID: taskID, Status: tasks.StatusInProgress, Worktree: path}); err != nil {
			_ = m.appendEvent("worktree.create.failed", wt, err, nil)
			return Worktree{}, err
		}
	}

	m.mu.Lock()
	m.items[name] = wt
	err = m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		_ = m.appendEvent("worktree.create.failed", wt, err, nil)
		return Worktree{}, err
	}
	_ = m.appendEvent("worktree.create.after", wt, nil, nil)
	return wt, nil
}

func (m *Manager) Run(name, command string) (string, error) {
	wt, err := m.Get(name)
	if err != nil {
		return "", err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = wt.Path
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	output := stdout.String() + stderr.String()
	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("command timed out after %s", commandTimeout)
	}
	if err != nil {
		return output, err
	}
	return output, nil
}

func (m *Manager) Keep(name string) (Worktree, error) {
	wt, err := m.setStatus(name, StatusKept)
	if err != nil {
		return Worktree{}, err
	}
	_ = m.appendEvent("worktree.keep", wt, nil, nil)
	return wt, nil
}

func (m *Manager) Remove(name string, force, completeTask bool) (Worktree, error) {
	wt, err := m.Get(name)
	if err != nil {
		return Worktree{}, err
	}
	_ = m.appendEvent("worktree.remove.before", wt, nil, map[string]any{"force": force, "complete_task": completeTask})
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wt.Path)
	if out, err := m.git(args...); err != nil {
		_ = m.appendEvent("worktree.remove.failed", wt, err, map[string]any{"output": out})
		return Worktree{}, fmt.Errorf("git worktree remove failed: %w\n%s", err, out)
	}
	wt.Status = StatusRemoved
	m.mu.Lock()
	m.items[wt.Name] = wt
	err = m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		return Worktree{}, err
	}
	if completeTask && m.tasks != nil {
		if _, err := m.tasks.Update(tasks.UpdateInput{ID: wt.TaskID, Status: tasks.StatusCompleted, ClearWorktree: true}); err != nil {
			_ = m.appendEvent("worktree.remove.failed", wt, err, nil)
			return Worktree{}, err
		}
		_ = m.appendEvent("task.completed", wt, nil, nil)
	}
	_ = m.appendEvent("worktree.remove.after", wt, nil, nil)
	return wt, nil
}

func (m *Manager) Get(name string) (Worktree, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Worktree{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	wt, ok := m.items[name]
	if !ok {
		return Worktree{}, fmt.Errorf("worktree not found: %s", name)
	}
	return wt, nil
}

func (m *Manager) List() []Worktree {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sorted(m.items)
}

func (m *Manager) Events(limit int) ([]Event, error) {
	file, err := os.Open(m.events)
	if os.IsNotExist(err) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func (m *Manager) setStatus(name, status string) (Worktree, error) {
	wt, err := m.Get(name)
	if err != nil {
		return Worktree{}, err
	}
	wt.Status = status
	m.mu.Lock()
	m.items[wt.Name] = wt
	err = m.persistLocked()
	m.mu.Unlock()
	return wt, err
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.index)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var index indexFile
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}
	for _, wt := range index.Worktrees {
		if wt.Name == "" {
			continue
		}
		m.items[wt.Name] = wt
	}
	return nil
}

func (m *Manager) persistLocked() error {
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(indexFile{Worktrees: sorted(m.items)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(m.index, data, 0o644)
}

func (m *Manager) appendEvent(kind string, wt Worktree, eventErr error, meta map[string]any) error {
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return err
	}
	event := Event{
		Type:      kind,
		Name:      wt.Name,
		TaskID:    wt.TaskID,
		Path:      wt.Path,
		Branch:    wt.Branch,
		CreatedAt: time.Now().Format(time.RFC3339),
		Meta:      meta,
	}
	if eventErr != nil {
		event.Error = eventErr.Error()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(m.events, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

func (m *Manager) git(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.root
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("git command timed out after %s", commandTimeout)
	}
	return string(out), err
}

func sorted(items map[string]Worktree) []Worktree {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Worktree, 0, len(names))
	for _, name := range names {
		out = append(out, items[name])
	}
	return out
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	name = strings.ReplaceAll(name, " ", "-")
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid worktree name %q", name)
	}
	return name, nil
}

func Register(reg *tools.Registry, manager *Manager) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("worktree_create", "Create a git worktree and bind it to a task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":    map[string]any{"type": "string"},
				"task_id": map[string]any{"type": "integer"},
			},
			"required": []string{"name", "task_id"},
		}),
		Handler: func(input map[string]any) (string, error) {
			wt, err := manager.Create(stringValue(input, "name"), intValue(input, "task_id"))
			return jsonOut(wt, err)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("worktree_run", "Run a shell command inside a named worktree.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":    map[string]any{"type": "string"},
				"command": map[string]any{"type": "string"},
			},
			"required": []string{"name", "command"},
		}),
		Handler: func(input map[string]any) (string, error) {
			return manager.Run(stringValue(input, "name"), stringValue(input, "command"))
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("worktree_keep", "Mark a worktree as kept for later manual review.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []string{"name"},
		}),
		Handler: func(input map[string]any) (string, error) {
			wt, err := manager.Keep(stringValue(input, "name"))
			return jsonOut(wt, err)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("worktree_remove", "Remove a worktree and optionally complete its bound task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":          map[string]any{"type": "string"},
				"force":         map[string]any{"type": "boolean"},
				"complete_task": map[string]any{"type": "boolean"},
			},
			"required": []string{"name"},
		}),
		Handler: func(input map[string]any) (string, error) {
			wt, err := manager.Remove(stringValue(input, "name"), boolValue(input, "force"), boolValue(input, "complete_task"))
			return jsonOut(wt, err)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("worktree_list", "List known worktrees from .worktrees/index.json.", map[string]any{"type": "object", "properties": map[string]any{}}),
		Handler: func(input map[string]any) (string, error) {
			return jsonOut(map[string]any{"worktrees": manager.List()}, nil)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("worktree_events", "Read recent worktree events.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"limit": map[string]any{"type": "integer"}},
		}),
		Handler: func(input map[string]any) (string, error) {
			events, err := manager.Events(intValue(input, "limit"))
			return jsonOut(map[string]any{"events": events}, err)
		},
	})
}

func jsonOut(value any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func stringValue(input map[string]any, name string) string {
	value, ok := input[name]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(input map[string]any, name string) int {
	value, ok := input[name]
	if !ok {
		return 0
	}
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

func boolValue(input map[string]any, name string) bool {
	value, ok := input[name]
	if !ok {
		return false
	}
	b, _ := value.(bool)
	return b
}
