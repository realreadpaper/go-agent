package autonomous

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

const (
	ActionWork     = "work"
	ActionShutdown = "shutdown"
)

type IdleConfig struct {
	PollInterval time.Duration
	Timeout      time.Duration
}

type IdleResult struct {
	Action  string      `json:"action"`
	Message string      `json:"message,omitempty"`
	Task    *tasks.Task `json:"task,omitempty"`
}

// Controller 实现自主队友的 IDLE phase。
// WORK phase 仍然由 agent.Loop 负责；当模型调用 idle 工具后，harness 暂时接管并轮询 inbox 和任务板。
type Controller struct {
	team   *team.Manager
	tasks  *tasks.Manager
	config IdleConfig
}

func NewController(teamManager *team.Manager, taskManager *tasks.Manager, config IdleConfig) *Controller {
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	return &Controller{team: teamManager, tasks: taskManager, config: config}
}

func (c *Controller) Wait(ctx context.Context, teammate team.Teammate) (IdleResult, error) {
	deadline := time.NewTimer(c.config.Timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(c.config.PollInterval)
	defer ticker.Stop()

	for {
		result, ok, err := c.checkOnce(teammate)
		if err != nil {
			return IdleResult{}, err
		}
		if ok {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return IdleResult{}, ctx.Err()
		case <-deadline.C:
			return IdleResult{Action: ActionShutdown, Message: "idle timeout"}, nil
		case <-ticker.C:
		}
	}
}

func (c *Controller) Claim(taskID int, teammate team.Teammate) (tasks.Task, error) {
	return c.tasks.Claim(taskID, teammate.Name)
}

func (c *Controller) checkOnce(teammate team.Teammate) (IdleResult, bool, error) {
	inbox, err := c.team.ReadInbox(teammate.Name)
	if err != nil {
		return IdleResult{}, false, err
	}
	if len(inbox) > 0 {
		return IdleResult{Action: ActionWork, Message: inboxMessage(inbox)}, true, nil
	}
	task, ok, err := c.tasks.ClaimReady(teammate.Name)
	if err != nil {
		return IdleResult{}, false, err
	}
	if ok {
		message := fmt.Sprintf("<auto-claimed>Task #%d: %s\n%s</auto-claimed>", task.ID, task.Subject, task.Description)
		return IdleResult{Action: ActionWork, Message: message, Task: &task}, true, nil
	}
	return IdleResult{}, false, nil
}

func inboxMessage(messages []team.Message) string {
	var b strings.Builder
	b.WriteString("<inbox>\n")
	for _, message := range messages {
		b.WriteString(fmt.Sprintf("from=%s at=%s\n%s\n\n", message.From, message.CreatedAt, message.Content))
	}
	b.WriteString("</inbox>")
	return b.String()
}

func IdentityHook(teammate team.Teammate, teamName string) agent.BeforeCallHook {
	return func(messages *[]llm.Message) error {
		if len(*messages) > 3 {
			return nil
		}
		if teamName == "" {
			teamName = "default"
		}
		identity := fmt.Sprintf("<identity>You are '%s', role: %s, team: %s. Continue your work.</identity>", teammate.Name, teammate.Role, teamName)
		*messages = append([]llm.Message{{Role: "user", Content: identity}}, (*messages)...)
		return nil
	}
}

func Register(reg *tools.Registry, controller *Controller, teammate team.Teammate) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("idle", "Enter autonomous idle mode: wait for inbox messages or claim ready tasks.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		Handler: func(input map[string]any) (string, error) {
			result, err := controller.Wait(context.Background(), teammate)
			return jsonOut(result, err)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("claim_task", "Claim one ready task by id for this teammate.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "integer"},
			},
			"required": []string{"task_id"},
		}),
		Handler: func(input map[string]any) (string, error) {
			task, err := controller.Claim(intValue(input, "task_id"), teammate)
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
