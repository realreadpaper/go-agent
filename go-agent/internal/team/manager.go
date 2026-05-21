package team

import (
	"context"
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
	StatusWorking = "working"
	StatusIdle    = "idle"
	StatusStopped = "stopped"
)

// Teammate 是 .team/config.json 中持久化的队友状态。
// cancel 函数不落盘，只在本进程内用于停止 goroutine。
type Teammate struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type Config struct {
	Members []Teammate `json:"members"`
}

// Runner 是队友 goroutine 依赖的执行器。
// CLI 中会用真实 agent loop；测试中用 fake runner，避免依赖网络。
type Runner interface {
	Run(ctx context.Context, teammate Teammate, prompt string) (string, error)
}

// Manager 维护 roster、message bus 和本进程内的队友 goroutine。
type Manager struct {
	mu      sync.Mutex
	root    string
	config  string
	bus     *MessageBus
	runner  Runner
	members map[string]Teammate
	cancel  map[string]context.CancelFunc
}

func NewManager(root string, runner Runner) (*Manager, error) {
	manager := &Manager{
		root:    root,
		config:  filepath.Join(root, ".team", "config.json"),
		bus:     NewMessageBus(root),
		runner:  runner,
		members: map[string]Teammate{},
		cancel:  map[string]context.CancelFunc{},
	}
	if err := manager.loadConfig(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Spawn(name, role, prompt string) (Teammate, error) {
	name = normalizeName(name)
	role = strings.TrimSpace(role)
	prompt = strings.TrimSpace(prompt)
	if name == "" {
		return Teammate{}, fmt.Errorf("name is required")
	}
	if role == "" {
		return Teammate{}, fmt.Errorf("role is required")
	}
	if prompt == "" {
		return Teammate{}, fmt.Errorf("prompt is required")
	}
	if m.runner == nil {
		return Teammate{}, fmt.Errorf("team runner is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	member := Teammate{Name: name, Role: role, Status: StatusWorking}

	m.mu.Lock()
	if oldCancel := m.cancel[name]; oldCancel != nil {
		oldCancel()
	}
	m.members[name] = member
	m.cancel[name] = cancel
	err := m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		cancel()
		return Teammate{}, err
	}

	go m.runTeammate(ctx, member, prompt)
	return member, nil
}

func (m *Manager) Get(name string) (Teammate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	member, ok := m.members[normalizeName(name)]
	if !ok {
		return Teammate{}, fmt.Errorf("teammate not found: %s", name)
	}
	return member, nil
}

func (m *Manager) Config() (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Config{Members: sortedMembers(m.members)}, nil
}

func (m *Manager) Send(from, to, content string) error {
	return m.bus.Send(Message{From: from, To: to, Content: content})
}

func (m *Manager) Broadcast(from, content string) error {
	config, err := m.Config()
	if err != nil {
		return err
	}
	for _, member := range config.Members {
		if err := m.Send(from, member.Name, content); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ReadInbox(name string) ([]Message, error) {
	return m.bus.ReadInbox(name)
}

func (m *Manager) Stop(name string) error {
	name = normalizeName(name)
	m.mu.Lock()
	cancel := m.cancel[name]
	if cancel != nil {
		cancel()
		delete(m.cancel, name)
	}
	member, ok := m.members[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("teammate not found: %s", name)
	}
	member.Status = StatusStopped
	m.members[name] = member
	err := m.persistLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) Bus() *MessageBus {
	return m.bus
}

func (m *Manager) runTeammate(ctx context.Context, member Teammate, prompt string) {
	summary, err := m.runner.Run(ctx, member, prompt)
	status := StatusIdle
	if err != nil && ctx.Err() != nil {
		status = StatusStopped
	}
	if strings.TrimSpace(summary) != "" && err == nil {
		_ = m.Send(member.Name, "lead", summary)
	}
	m.mu.Lock()
	current := m.members[member.Name]
	current.Status = status
	m.members[member.Name] = current
	delete(m.cancel, member.Name)
	_ = m.persistLocked()
	m.mu.Unlock()
}

func (m *Manager) loadConfig() error {
	data, err := os.ReadFile(m.config)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	for _, member := range config.Members {
		member.Name = normalizeName(member.Name)
		if member.Name == "" {
			continue
		}
		if member.Status == "" {
			member.Status = StatusIdle
		}
		m.members[member.Name] = member
	}
	return nil
}

func (m *Manager) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.config), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Config{Members: sortedMembers(m.members)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(m.config, data, 0o644)
}

func sortedMembers(members map[string]Teammate) []Teammate {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Teammate, 0, len(names))
	for _, name := range names {
		out = append(out, members[name])
	}
	return out
}

func Register(reg *tools.Registry, manager *Manager) {
	RegisterForSender(reg, manager, "lead")
}

func RegisterForSender(reg *tools.Registry, manager *Manager, sender string) {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		sender = "lead"
	}
	reg.Register(tools.Tool{
		Spec: tools.Spec("spawn_teammate", "Spawn a persistent teammate agent with a role and initial prompt.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":   map[string]any{"type": "string"},
				"role":   map[string]any{"type": "string"},
				"prompt": map[string]any{"type": "string"},
			},
			"required": []string{"name", "role", "prompt"},
		}),
		Handler: func(input map[string]any) (string, error) {
			member, err := manager.Spawn(stringValue(input, "name"), stringValue(input, "role"), stringValue(input, "prompt"))
			return jsonOut(member, err)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("list_teammates", "List persistent teammate roster and statuses.", map[string]any{"type": "object", "properties": map[string]any{}}),
		Handler: func(input map[string]any) (string, error) {
			config, err := manager.Config()
			return jsonOut(config, err)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("send_message", "Send a message to one teammate inbox.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":      map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"to", "content"},
		}),
		Handler: func(input map[string]any) (string, error) {
			to := stringValue(input, "to")
			if err := manager.Send(sender, to, stringValue(input, "content")); err != nil {
				return "", err
			}
			return fmt.Sprintf("sent to %s", to), nil
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("broadcast", "Send a message to all teammates.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"content": map[string]any{"type": "string"}},
			"required":   []string{"content"},
		}),
		Handler: func(input map[string]any) (string, error) {
			if err := manager.Broadcast(sender, stringValue(input, "content")); err != nil {
				return "", err
			}
			return "broadcast sent", nil
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("read_inbox", "Drain and return one teammate inbox.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []string{"name"},
		}),
		Handler: func(input map[string]any) (string, error) {
			messages, err := manager.ReadInbox(stringValue(input, "name"))
			return jsonOut(messages, err)
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
