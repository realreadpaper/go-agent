package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"learn-claude-code-go/internal/tools"
)

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	maxTodoItems     = 20
)

// Item 是模型写入 TodoWrite 的结构化计划项。
// Content 是用户和模型都能读懂的任务描述；ActiveForm 用来表达当前正在做这件事时的动作表述。
type Item struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}

// Manager 保存一次 agent 会话内的 todo 状态。
// 默认 NewManager 只在内存中保存，适合单元测试和不需要落盘的演示。
// NewPersistentManager 会把本次运行的最终 TodoWrite 状态写入 .goagent/todo。
// 中间过程由 CLI 日志展示，文件里只保留最后一次 todo，避免初学者被历史快照干扰。
type Manager struct {
	mu        sync.Mutex
	items     []Item
	storeDir  string
	storeFile string
}

func NewManager() *Manager {
	return &Manager{}
}

// NewPersistentManager 创建会把 todo 快照落盘的 Manager。
// workdir 通常是当前项目目录；最终文件会写到 workdir/.goagent/todo/。
// 一个 Manager 对应一次运行；多次 Update 会覆盖同一个文件，只留下最终状态。
func NewPersistentManager(workdir string) *Manager {
	now := time.Now()
	return &Manager{
		storeDir:  filepath.Join(workdir, ".goagent", "todo"),
		storeFile: formatSnapshotFileName(now),
	}
}

func (m *Manager) StoreDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storeDir
}

// Update 校验并替换整个 todo 列表。
// TodoWrite 采用“整体写入”而不是“局部 patch”，这样模型每次都要显式声明完整计划状态。
func (m *Manager) Update(items []Item) (string, error) {
	if len(items) > maxTodoItems {
		return "", fmt.Errorf("at most 20 todos are allowed")
	}

	validated := make([]Item, 0, len(items))
	inProgress := 0
	for i, item := range items {
		item.Content = strings.TrimSpace(item.Content)
		item.Status = strings.TrimSpace(item.Status)
		item.ActiveForm = strings.TrimSpace(item.ActiveForm)
		if item.Content == "" {
			return "", fmt.Errorf("todo %d content is required", i+1)
		}
		if item.Status == "" {
			item.Status = StatusPending
		}
		switch item.Status {
		case StatusPending, StatusCompleted:
		case StatusInProgress:
			inProgress++
		default:
			return "", fmt.Errorf("invalid todo status %q", item.Status)
		}
		validated = append(validated, item)
	}
	if inProgress > 1 {
		return "", fmt.Errorf("only one todo can be in_progress")
	}

	m.mu.Lock()
	m.items = validated
	rendered := render(validated)
	storeDir := m.storeDir
	storeFile := m.storeFile
	snapshotItems := append([]Item(nil), validated...)
	m.mu.Unlock()
	if storeDir != "" {
		if err := writeSnapshot(storeDir, storeFile, snapshotItems, rendered); err != nil {
			return "", err
		}
	}
	return rendered, nil
}

func (m *Manager) Render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return render(m.items)
}

func render(items []Item) string {
	if len(items) == 0 {
		return "(no todos)"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		marker := "[ ]"
		switch item.Status {
		case StatusInProgress:
			marker = "[>]"
		case StatusCompleted:
			marker = "[x]"
		}
		lines = append(lines, marker+" "+item.Content)
	}
	return strings.Join(lines, "\n")
}

// snapshotFile 是写入 .goagent/todo 的教学快照格式。
// items 保存结构化 todo，rendered 保存 CLI 中看到的人类可读文本。
type snapshotFile struct {
	CreatedAt string `json:"created_at"`
	Items     []Item `json:"items"`
	Rendered  string `json:"rendered"`
}

func writeSnapshot(storeDir, storeFile string, items []Item, rendered string) error {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return fmt.Errorf("create todo store: %w", err)
	}
	now := time.Now()
	snapshot := snapshotFile{
		CreatedAt: formatSnapshotTime(now),
		Items:     items,
		Rendered:  rendered,
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode todo snapshot: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(storeDir, storeFile)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write todo snapshot: %w", err)
	}
	return nil
}

func formatSnapshotTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05 -07:00")
}

func formatSnapshotFileName(t time.Time) string {
	local := t.Local()
	return fmt.Sprintf("todo-%s-%06d.json", local.Format("2006-01-02-15-04-05"), local.Nanosecond()/1_000)
}

// Register 把 todo 工具接入通用 tools.Registry。
// agent loop 不知道 todo 的内部状态，只会把模型传来的 items 参数交给这个 handler。
func Register(reg *tools.Registry, manager *Manager) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("todo", "Update the session todo list. Use it before and during multi-step work.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "Complete todo list. Each item has content, status, and optional activeForm.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content":    map[string]any{"type": "string"},
							"status":     map[string]any{"type": "string", "enum": []string{StatusPending, StatusInProgress, StatusCompleted}},
							"activeForm": map[string]any{"type": "string"},
						},
						"required": []string{"content", "status"},
					},
				},
			},
			"required": []string{"items"},
		}),
		Handler: func(input map[string]any) (string, error) {
			items, err := parseItems(input["items"])
			if err != nil {
				return "", err
			}
			return manager.Update(items)
		},
	})
}

func parseItems(raw any) ([]Item, error) {
	if raw == nil {
		return nil, fmt.Errorf("items is required")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("items must be an array of todo items: %w", err)
	}
	return items, nil
}
