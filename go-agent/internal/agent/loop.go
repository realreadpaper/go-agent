package agent

import (
	"fmt"
	"io"
	"regexp"

	"learn-claude-code-go/internal/llm"
)

const maxToolResultChars = 50000

var (
	apiKeyAssignmentPattern = regexp.MustCompile(`(?i)([A-Z0-9_]*API[_-]?KEY=)[^\s"'<>]+`)
	skSecretPattern         = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
)

// BeforeCallHook 在每次请求 LLM 前运行。
// 后续章节会用它注入 todo reminder、上下文压缩摘要、队友身份等 harness 状态。
type BeforeCallHook func(messages *[]llm.Message) error

// AfterToolHook 在某个工具执行后运行。
// 它只接收工具名，用来做轻量统计或清零 reminder 计数，不应该在这里改写工具输出。
type AfterToolHook func(name string)

// ToolRunner 是 agent loop 和具体工具集合之间的边界。
// Loop 只依赖这两个能力：把工具 schema 暴露给模型，以及按模型给出的工具名执行本地 handler。
type ToolRunner interface {
	Specs() []llm.ToolSpec
	Run(name string, input map[string]any) string
}

// Loop 是最小 agent harness 的核心控制器。
// 它负责反复调用模型、识别 tool_use、执行本地工具、再把 tool_result 追加回 messages。
// 注意：模型只“请求”调用工具，真正的文件系统、shell、任务状态修改都发生在 Go harness 内。
type Loop struct {
	Client     llm.Client
	Model      string
	System     string
	Tools      ToolRunner
	MaxTokens  int
	MaxRounds  int
	BeforeCall []BeforeCallHook
	AfterTool  []AfterToolHook
	// Trace 是可选调试输出。CLI 可以把它接到 stderr，让读者看到每轮模型调用和工具执行过程；
	// 库调用或单元测试保持 nil 时完全静默，不影响最终回答的 stdout。
	Trace io.Writer
}

// Run 从已有 messages 开始执行 agent loop，并返回完整 transcript 与最后一次 LLM 响应。
// 退出条件只有两个：模型不再返回 tool_use，或超过 MaxRounds 防止模型无限要求工具调用。
func (l *Loop) Run(messages []llm.Message) ([]llm.Message, llm.Response, error) {
	var resp llm.Response
	rounds := l.MaxRounds
	if rounds == 0 {
		rounds = 50
	}

	for i := 0; i < rounds; i++ {
		l.tracef("[agent] round=%d messages=%d\n", i+1, len(messages))
		// BeforeCall hook 是“请求模型前”的统一扩展点。
		// 这让后续功能可以通过改写 messages 注入提醒或摘要，而不必改动主循环。
		for _, hook := range l.BeforeCall {
			if err := hook(&messages); err != nil {
				return messages, resp, err
			}
		}

		var specs []llm.ToolSpec
		if l.Tools != nil {
			specs = l.Tools.Specs()
		}
		l.tracef("[agent] request model=%s tools=%d max_tokens=%d\n", l.Model, len(specs), l.MaxTokens)
		// 每轮都把完整消息历史和当前可用工具 schema 发给 LLM。
		// provider adapter 只负责协议转换，不能在这里或 adapter 里直接执行工具。
		var err error
		resp, err = l.Client.Create(llm.Request{
			Model:     l.Model,
			System:    l.System,
			Messages:  messages,
			Tools:     specs,
			MaxTokens: l.MaxTokens,
		})
		if err != nil {
			return messages, resp, err
		}
		l.tracef("[agent] response stop_reason=%s blocks=%d\n", resp.StopReason, len(resp.Content))
		l.traceResponse(resp)
		// assistant message 必须保留原始 tool_use block。
		// 下一轮 provider adapter 需要它把工具结果和正确的 tool_use id 对上。
		messages = append(messages, llm.Message{Role: "assistant", Content: resp.Content})
		if resp.StopReason != "tool_use" {
			return messages, resp, nil
		}

		results := make([]llm.ToolResult, 0)
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			l.tracef("[agent] tool_use name=%s id=%s input=%s\n", block.Name, block.ID, summarizeMap(block.Input, 500))
			// 只有 harness 可以执行工具。未知工具、handler error、空输出都由 Registry 统一格式化为字符串，
			// 这样模型总能收到一个 tool_result，而不是让 Go error 打断整个对话。
			output := "Error: no tools registered"
			if l.Tools != nil {
				output = l.Tools.Run(block.Name, block.Input)
			}
			// 工具输出可能非常长，例如 cat 大文件或命令打印日志。
			// 截断在 loop 层做一遍，避免把下一轮请求撑爆；Registry 层也会为普通工具做同样保护。
			if len(output) > maxToolResultChars {
				output = output[:maxToolResultChars]
			}
			l.tracef("[agent] tool_result name=%s chars=%d preview=%q\n", block.Name, len(output), summarizeString(output, 300))
			for _, hook := range l.AfterTool {
				hook(block.Name)
			}
			results = append(results, llm.ToolResult{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   output,
			})
		}
		// tool_result 在 Anthropic/OpenAI 语义中都作为 user-side content 回传。
		// 这一步完成后，下一轮模型才能基于真实命令输出继续推理。
		messages = append(messages, llm.Message{Role: "user", Content: results})
	}

	return messages, resp, fmt.Errorf("agent loop exceeded MaxRounds=%d", rounds)
}

func (l *Loop) tracef(format string, args ...any) {
	if l.Trace == nil {
		return
	}
	_, _ = fmt.Fprintf(l.Trace, format, args...)
}

func (l *Loop) traceResponse(resp llm.Response) {
	for i, block := range resp.Content {
		switch block.Type {
		case "text":
			l.tracef("[agent] content[%d] type=text text=%q\n", i, summarizeString(block.Text, 1000))
		case "tool_use":
			l.tracef("[agent] content[%d] type=tool_use name=%s id=%s input=%s\n", i, block.Name, block.ID, summarizeMap(block.Input, 500))
		default:
			l.tracef("[agent] content[%d] type=%s\n", i, block.Type)
		}
	}
	if resp.RawBody != "" {
		l.tracef("[agent] raw_api=%s\n", summarizeString(resp.RawBody, 4000))
	}
}

func summarizeMap(input map[string]any, limit int) string {
	if input == nil {
		return "{}"
	}
	return summarizeString(fmt.Sprintf("%v", input), limit)
}

func summarizeString(text string, limit int) string {
	text = redactSecrets(text)
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func redactSecrets(text string) string {
	text = apiKeyAssignmentPattern.ReplaceAllString(text, `${1}<redacted>`)
	return skSecretPattern.ReplaceAllString(text, "sk-<redacted>")
}
