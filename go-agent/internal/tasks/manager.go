package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"learn-claude-code-go/internal/tools"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
)

// allowedTransitions 是本地状态机转换表。
// 使用显式表而不是散落的 if/else，是为了让长期任务的生命周期规则一眼可见、可测试、可扩展。
var allowedTransitions = map[string]map[string]bool{
	StatusPending: {
		StatusInProgress: true,
		StatusCompleted:  true,
	},
	StatusInProgress: {
		StatusPending:   true,
		StatusCompleted: true,
	},
	StatusCompleted: {},
}

// Task 是跨会话保存的长期工作单元。
// 它和 TodoWrite 的区别是：Task 会落盘、有依赖、有 owner/worktree，后续后台 agent 和团队协作都能复用它。
type Task struct {
	ID          int    `json:"id"`
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"`
	BlockedBy   []int  `json:"blockedBy"`
	Owner       string `json:"owner"`
	Worktree    string `json:"worktree"`
}

type CreateInput struct {
	Subject     string
	Description string
	BlockedBy   []int
	Owner       string
	Worktree    string
}

type UpdateInput struct {
	ID              int
	Status          string
	AddBlockedBy    []int
	RemoveBlockedBy []int
	Owner           string
	Worktree        string
	ClearWorktree   bool
}

// Manager 管理 .tasks 目录中的任务图。
// 每个 task 独立成一个 JSON 文件，便于恢复，也便于未来多个 agent 用文件系统协调。
type Manager struct {
	mu     sync.Mutex
	root   string
	dir    string
	nextID int
	tasks  map[int]Task
}

func NewManager(root string) *Manager {
	return &Manager{
		root:   root,
		dir:    filepath.Join(root, ".tasks"),
		nextID: 1,
		tasks:  map[int]Task{},
	}
}

func LoadManager(root string) (*Manager, error) {
	manager := NewManager(root)
	entries, err := os.ReadDir(manager.dir)
	if os.IsNotExist(err) {
		return manager, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "task_") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(manager.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			return nil, err
		}
		manager.tasks[task.ID] = task
		if task.ID >= manager.nextID {
			manager.nextID = task.ID + 1
		}
	}
	return manager, nil
}

func (m *Manager) Dir() string {
	return m.dir
}

func (m *Manager) Create(input CreateInput) (Task, error) {
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		return Task{}, fmt.Errorf("subject is required")
	}
	status := StatusPending
	if err := validateStatus(status); err != nil {
		return Task{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task := Task{
		ID:          m.nextID,
		Subject:     subject,
		Description: strings.TrimSpace(input.Description),
		Status:      status,
		BlockedBy:   uniqueInts(input.BlockedBy),
		Owner:       strings.TrimSpace(input.Owner),
		Worktree:    strings.TrimSpace(input.Worktree),
	}
	m.nextID++
	m.tasks[task.ID] = task
	if err := m.persistLocked(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (m *Manager) Update(input UpdateInput) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[input.ID]
	if !ok {
		return Task{}, fmt.Errorf("task not found: %d", input.ID)
	}
	completedNow := false
	if strings.TrimSpace(input.Status) != "" {
		nextStatus, transitioned, err := transitionStatus(task.Status, input.Status)
		if err != nil {
			return Task{}, err
		}
		task.Status = nextStatus
		completedNow = transitioned && nextStatus == StatusCompleted
	}
	task.BlockedBy = uniqueInts(append(task.BlockedBy, input.AddBlockedBy...))
	task.BlockedBy = removeInts(task.BlockedBy, input.RemoveBlockedBy)
	if input.Owner != "" {
		task.Owner = strings.TrimSpace(input.Owner)
	}
	if input.Worktree != "" {
		task.Worktree = strings.TrimSpace(input.Worktree)
	}
	if input.ClearWorktree {
		task.Worktree = ""
	}
	m.tasks[task.ID] = task
	if err := m.persistLocked(task); err != nil {
		return Task{}, err
	}
	if completedNow {
		if err := m.unblockDependentsLocked(task.ID); err != nil {
			return Task{}, err
		}
	}
	return task, nil
}

func (m *Manager) Get(id int) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task not found: %d", id)
	}
	return task, nil
}

func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sortedTasks(m.tasks)
}

func (m *Manager) ListReady() ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ready []Task
	for _, task := range sortedTasks(m.tasks) {
		if task.Status == StatusPending && task.Owner == "" && len(task.BlockedBy) == 0 {
			ready = append(ready, task)
		}
	}
	return ready, nil
}

func (m *Manager) ClaimReady(owner string) (Task, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, false, fmt.Errorf("owner is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range sortedTasks(m.tasks) {
		if !isReadyUnclaimed(task) {
			continue
		}
		task.Owner = owner
		task.Status = StatusInProgress
		m.tasks[task.ID] = task
		if err := m.persistLocked(task); err != nil {
			return Task{}, false, err
		}
		return task, true, nil
	}
	return Task{}, false, nil
}

func (m *Manager) Claim(id int, owner string) (Task, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, fmt.Errorf("owner is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("task not found: %d", id)
	}
	if !isReadyUnclaimed(task) {
		return Task{}, fmt.Errorf("task %d is not ready to claim", id)
	}
	task.Owner = owner
	task.Status = StatusInProgress
	m.tasks[task.ID] = task
	if err := m.persistLocked(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (m *Manager) persistLocked(task Task) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(m.dir, fmt.Sprintf("task_%d.json", task.ID)), data, 0o644)
}

func isReadyUnclaimed(task Task) bool {
	return task.Status == StatusPending && task.Owner == "" && len(task.BlockedBy) == 0
}

func (m *Manager) unblockDependentsLocked(completedID int) error {
	for id, task := range m.tasks {
		updated := removeInts(task.BlockedBy, []int{completedID})
		if len(updated) == len(task.BlockedBy) {
			continue
		}
		task.BlockedBy = updated
		m.tasks[id] = task
		if err := m.persistLocked(task); err != nil {
			return err
		}
	}
	return nil
}

func validateStatus(status string) error {
	if _, ok := allowedTransitions[status]; ok {
		return nil
	}
	return fmt.Errorf("invalid task status %q", status)
}

func transitionStatus(current, next string) (string, bool, error) {
	next = strings.TrimSpace(next)
	if err := validateStatus(current); err != nil {
		return "", false, err
	}
	if err := validateStatus(next); err != nil {
		return "", false, err
	}
	if current == next {
		return current, false, nil
	}
	if !allowedTransitions[current][next] {
		return "", false, fmt.Errorf("invalid task transition %s -> %s", current, next)
	}
	return next, true, nil
}

func sortedTasks(tasks map[int]Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func uniqueInts(values []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, value := range values {
		if value <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

func removeInts(values, remove []int) []int {
	if len(remove) == 0 {
		return values
	}
	blocked := map[int]bool{}
	for _, value := range remove {
		blocked[value] = true
	}
	var out []int
	for _, value := range values {
		if !blocked[value] {
			out = append(out, value)
		}
	}
	return out
}

func Register(reg *tools.Registry, manager *Manager) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("task_create", "Create a persistent task in the local task graph.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subject":     map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"blocked_by":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				"owner":       map[string]any{"type": "string"},
				"worktree":    map[string]any{"type": "string"},
			},
			"required": []string{"subject"},
		}),
		Handler: func(input map[string]any) (string, error) {
			task, err := manager.Create(CreateInput{
				Subject:     stringValue(input, "subject"),
				Description: stringValue(input, "description"),
				BlockedBy:   intSliceValue(input, "blocked_by"),
				Owner:       stringValue(input, "owner"),
				Worktree:    stringValue(input, "worktree"),
			})
			return jsonOut(task, err)
		},
	})

	reg.Register(tools.Tool{
		Spec: tools.Spec("task_update", "Update a persistent task status, dependencies, owner, or worktree.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id":           map[string]any{"type": "integer"},
				"status":            map[string]any{"type": "string", "enum": []string{StatusPending, StatusInProgress, StatusCompleted}},
				"add_blocked_by":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				"remove_blocked_by": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
				"owner":             map[string]any{"type": "string"},
				"worktree":          map[string]any{"type": "string"},
			},
			"required": []string{"task_id"},
		}),
		Handler: func(input map[string]any) (string, error) {
			id := intValue(input, "task_id")
			if id <= 0 {
				return "", fmt.Errorf("task_id is required")
			}
			task, err := manager.Update(UpdateInput{
				ID:              id,
				Status:          stringValue(input, "status"),
				AddBlockedBy:    intSliceValue(input, "add_blocked_by"),
				RemoveBlockedBy: intSliceValue(input, "remove_blocked_by"),
				Owner:           stringValue(input, "owner"),
				Worktree:        stringValue(input, "worktree"),
			})
			return jsonOut(task, err)
		},
	})

	reg.Register(tools.Tool{
		Spec: tools.Spec("task_get", "Read one persistent task by id.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"task_id": map[string]any{"type": "integer"}},
			"required":   []string{"task_id"},
		}),
		Handler: func(input map[string]any) (string, error) {
			task, err := manager.Get(intValue(input, "task_id"))
			return jsonOut(task, err)
		},
	})

	reg.Register(tools.Tool{
		Spec: tools.Spec("task_list", "List all persistent tasks and currently ready tasks.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		Handler: func(input map[string]any) (string, error) {
			ready, err := manager.ListReady()
			if err != nil {
				return "", err
			}
			return jsonOut(map[string]any{"tasks": manager.List(), "ready": ready}, nil)
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

func intSliceValue(input map[string]any, name string) []int {
	raw, ok := input[name]
	if !ok || raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []int:
		return values
	case []any:
		out := make([]int, 0, len(values))
		for _, value := range values {
			switch n := value.(type) {
			case int:
				out = append(out, n)
			case int64:
				out = append(out, int(n))
			case float64:
				out = append(out, int(n))
			}
		}
		return out
	default:
		return nil
	}
}
