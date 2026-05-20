package background

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"

	commandTimeout      = 300 * time.Second
	maxCommandOutput    = 50_000
	maxNotificationText = 500
)

// Task 是后台命令的可查询状态。
// command 在 goroutine 中运行，主 agent loop 只通过 Check 和通知队列观察它。
type Task struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Status   string `json:"status"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
}

// Notification 是后台任务完成后注入下一轮上下文的短消息。
// goroutine 不直接驱动模型，只把事件放入 manager，保持 agent.Loop 是唯一控制点。
type Notification struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

// Manager 管理后台命令和完成通知。
// tasks 受 mutex 保护；notifications 用 buffered channel，避免命令完成时阻塞 goroutine。
type Manager struct {
	mu            sync.Mutex
	workdir       string
	nextID        int
	tasks         map[string]Task
	notifications chan Notification
}

func NewManager(workdir string) *Manager {
	return &Manager{
		workdir:       workdir,
		nextID:        1,
		tasks:         map[string]Task{},
		notifications: make(chan Notification, 128),
	}
}

// Run 启动后台命令并立刻返回 task id。
// 命令实际执行发生在 goroutine 中，所以模型可以继续请求其他工具。
func (m *Manager) Run(command string) (Task, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return Task{}, fmt.Errorf("command is required")
	}
	if err := rejectDangerousCommand(command); err != nil {
		return Task{}, err
	}
	m.mu.Lock()
	id := fmt.Sprintf("bg-%06d", m.nextID)
	m.nextID++
	task := Task{
		ID:      id,
		Command: command,
		Status:  StatusRunning,
		Started: time.Now().Format(time.RFC3339),
	}
	m.tasks[id] = task
	m.mu.Unlock()

	go m.runCommand(task)
	return task, nil
}

func (m *Manager) Check(id string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[strings.TrimSpace(id)]
	if !ok {
		return Task{}, fmt.Errorf("background task not found: %s", id)
	}
	return task, nil
}

func (m *Manager) DrainNotifications() []Notification {
	var out []Notification
	for {
		select {
		case notification := <-m.notifications:
			out = append(out, notification)
		default:
			return out
		}
	}
}

// BeforeCall 是 agent.Loop 的 hook。
// 它把后台完成事件作为普通 user message 注入，下一轮模型才能基于结果继续决策。
func (m *Manager) BeforeCall(messages *[]llm.Message) error {
	notifications := m.DrainNotifications()
	if len(notifications) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("<background-results>\n")
	for _, notification := range notifications {
		b.WriteString(fmt.Sprintf("[%s] %s\n%s\n", notification.TaskID, notification.Status, notification.Summary))
	}
	b.WriteString("</background-results>")
	*messages = append(*messages, llm.Message{Role: "user", Content: b.String()})
	return nil
}

func (m *Manager) runCommand(task Task) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", task.Command)
	if m.workdir != "" {
		if abs, err := filepath.Abs(m.workdir); err == nil {
			cmd.Dir = abs
		}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := truncate(stdout.String()+stderr.String(), maxCommandOutput)

	task.Output = output
	task.Finished = time.Now().Format(time.RFC3339)
	if ctx.Err() == context.DeadlineExceeded {
		task.Status = StatusFailed
		task.Error = "command timed out after 300s"
	} else if err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
	} else {
		task.Status = StatusCompleted
	}

	m.mu.Lock()
	m.tasks[task.ID] = task
	m.mu.Unlock()

	summary := output
	if task.Error != "" {
		summary = task.Error + "\n" + output
	}
	m.notifications <- Notification{
		TaskID:  task.ID,
		Status:  task.Status,
		Summary: truncate(strings.TrimSpace(summary), maxNotificationText),
	}
}

func Register(reg *tools.Registry, manager *Manager) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("background_run", "Run a bash command in the background and return a task id immediately.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to run asynchronously."},
			},
			"required": []string{"command"},
		}),
		Handler: func(input map[string]any) (string, error) {
			task, err := manager.Run(stringValue(input, "command"))
			return jsonOut(task, err)
		},
	})

	reg.Register(tools.Tool{
		Spec: tools.Spec("background_check", "Check a background command by task id.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Background task id returned by background_run."},
			},
			"required": []string{"task_id"},
		}),
		Handler: func(input map[string]any) (string, error) {
			task, err := manager.Check(stringValue(input, "task_id"))
			return jsonOut(task, err)
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

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func rejectDangerousCommand(command string) error {
	for _, fragment := range []string{"rm -rf /", "sudo", "shutdown", "reboot", "> /dev/"} {
		if strings.Contains(command, fragment) {
			return fmt.Errorf("dangerous command rejected: contains %q", fragment)
		}
	}
	return nil
}
