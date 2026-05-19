package llm

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ContentBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	Text  string         `json:"text,omitempty"`
}

type ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type Response struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	// RawBody 只在显式开启原始 API trace 时填充。
	// 它用于调试 provider adapter 的协议映射，普通业务逻辑应该继续读取 Content/StopReason。
	RawBody string `json:"raw_body,omitempty"`
}

type Client interface {
	Create(req Request) (Response, error)
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
}
