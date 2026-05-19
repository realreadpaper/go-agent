package agent

import "learn-claude-code-go/internal/llm"

const todoReminderText = "<reminder>Update your todos.</reminder>"

// TodoNag 是 TodoWrite 的轻量提醒器。
// 它不管理 todo 内容，只观察模型是否调用过 todo 工具；真正的状态校验在 internal/todo.Manager 中完成。
type TodoNag struct {
	threshold       int
	roundsSinceTodo int
}

// WithTodoNag 创建可挂到 Loop.BeforeCall / Loop.AfterTool 的 hook 对象。
// threshold 表示连续多少轮工具调用没有更新 todo 后，在下一次 LLM 请求前注入 reminder。
func WithTodoNag(threshold int) *TodoNag {
	if threshold <= 0 {
		threshold = 3
	}
	return &TodoNag{threshold: threshold}
}

func (n *TodoNag) BeforeCall(messages *[]llm.Message) error {
	if n.roundsSinceTodo < n.threshold {
		return nil
	}
	// Go 当前的 message 模型没有“在 []ToolResult 前插入 text block”的混合 content 类型。
	// 因此这里追加一个独立 user reminder message；语义等价于在下一轮请求前提醒模型更新 todo。
	*messages = append(*messages, llm.Message{
		Role:    "user",
		Content: todoReminderText,
	})
	n.roundsSinceTodo = 0
	return nil
}

func (n *TodoNag) AfterTool(name string) {
	if name == "todo" {
		n.roundsSinceTodo = 0
		return
	}
	n.roundsSinceTodo++
}
