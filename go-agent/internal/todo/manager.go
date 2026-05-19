package todo

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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
// 这是短期规划板，不负责跨进程恢复；后续持久任务系统会处理长期任务。
type Manager struct {
	mu    sync.Mutex
	items []Item
}

func NewManager() *Manager {
	return &Manager{}
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
	m.mu.Unlock()
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
