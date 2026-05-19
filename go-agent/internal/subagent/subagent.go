package subagent

import (
	"fmt"
	"io"
	"strings"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

// Runner 是一次性子 agent 执行器。
// 它和父 agent 共享 LLM client，但使用全新的 messages，因此子 agent 读文件、跑命令的中间历史不会污染父上下文。
type Runner struct {
	Client    llm.Client
	Model     string
	System    string
	Tools     *tools.Registry
	MaxTokens int
	MaxRounds int
	Trace     io.Writer
}

// Run 用一个新的 user prompt 启动子 agent，并只返回最终文本摘要。
// 父 agent 收到的是这个摘要字符串，而不是子 agent 的完整 transcript。
func (r *Runner) Run(prompt string) (string, error) {
	loop := &agent.Loop{
		Client:    r.Client,
		Model:     r.Model,
		System:    r.System,
		Tools:     r.Tools,
		MaxTokens: r.MaxTokens,
		MaxRounds: r.MaxRounds,
		Trace:     r.Trace,
	}
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return "", err
	}
	return summaryText(resp), nil
}

func summaryText(resp llm.Response) string {
	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "(no summary)"
	}
	return text
}

// TaskRunner 是父端 task 工具依赖的最小接口。
// 用接口而不是具体 Runner，方便测试，也让未来可替换成并行 runner 或远端 runner。
type TaskRunner interface {
	Run(prompt string) (string, error)
}

// RegisterTask 只注册在父 agent 的工具集中。
// 子 agent 的工具集不应包含 task，否则模型可以递归创建子 agent，教学 harness 很容易失控。
func RegisterTask(reg *tools.Registry, runner TaskRunner) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("task", "Run a subagent with fresh context and return only its final summary.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "The focused task for the subagent to perform.",
				},
			},
			"required": []string{"prompt"},
		}),
		Handler: func(input map[string]any) (string, error) {
			prompt, err := promptArg(input)
			if err != nil {
				return "", err
			}
			return runner.Run(prompt)
		},
	})
}

func promptArg(input map[string]any) (string, error) {
	value, ok := input["prompt"]
	if !ok {
		return "", fmt.Errorf("prompt is required")
	}
	prompt, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("prompt must be a string")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	return prompt, nil
}
