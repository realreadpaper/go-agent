package llm

import (
	"encoding/json"
	"net/http"
)

type AnthropicCompatClient struct {
	cfg        Config
	httpClient *http.Client
}

func NewAnthropicCompatClient(cfg Config, httpClient *http.Client) *AnthropicCompatClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &AnthropicCompatClient{cfg: cfg, httpClient: httpClient}
}

type anthropicRequest struct {
	Model     string           `json:"model"`
	System    string           `json:"system,omitempty"`
	Messages  []map[string]any `json:"messages"`
	Tools     []map[string]any `json:"tools,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
}

func (c *AnthropicCompatClient) Create(req Request) (Response, error) {
	body := anthropicRequest{
		Model:     firstNonEmpty(req.Model, c.cfg.Model),
		System:    req.System,
		Messages:  toAnthropicMessages(req.Messages),
		Tools:     toAnthropicTools(req.Tools),
		MaxTokens: firstNonZero(req.MaxTokens, c.cfg.MaxTokens),
	}
	headers := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	if c.cfg.APIKey != "" {
		headers["x-api-key"] = c.cfg.APIKey
	}
	raw, err := doJSON(c.httpClient, http.MethodPost, joinURL(c.cfg.BaseURL, "/v1/messages"), c.cfg.APIKey, headers, body)
	if err != nil {
		return Response{}, err
	}
	return parseAnthropic(raw)
}

func toAnthropicTools(specs []ToolSpec) []map[string]any {
	tools := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, map[string]any{
			"name":         spec.Name,
			"description":  spec.Description,
			"input_schema": spec.InputSchema,
		})
	}
	return tools
}

func toAnthropicMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch content := msg.Content.(type) {
		case string:
			out = append(out, map[string]any{"role": msg.Role, "content": content})
		case []ToolResult:
			parts := make([]map[string]any, 0, len(content))
			for _, result := range content {
				parts = append(parts, map[string]any{
					"type":        "tool_result",
					"tool_use_id": result.ToolUseID,
					"content":     result.Content,
				})
			}
			out = append(out, map[string]any{"role": "user", "content": parts})
		case []ContentBlock:
			parts := make([]map[string]any, 0, len(content))
			for _, block := range content {
				switch block.Type {
				case "text":
					parts = append(parts, map[string]any{"type": "text", "text": block.Text})
				case "tool_use":
					parts = append(parts, map[string]any{
						"type":  "tool_use",
						"id":    block.ID,
						"name":  block.Name,
						"input": block.Input,
					})
				}
			}
			out = append(out, map[string]any{"role": msg.Role, "content": parts})
		}
	}
	return out
}

func parseAnthropic(raw []byte) (Response, error) {
	var envelope struct {
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Response{}, err
	}
	resp := Response{StopReason: firstNonEmpty(envelope.StopReason, "end_turn")}
	for _, part := range envelope.Content {
		switch part.Type {
		case "text":
			resp.Content = append(resp.Content, ContentBlock{Type: "text", Text: part.Text})
		case "tool_use":
			resp.Content = append(resp.Content, ContentBlock{
				Type:  "tool_use",
				ID:    part.ID,
				Name:  part.Name,
				Input: part.Input,
			})
			resp.StopReason = "tool_use"
		}
	}
	return resp, nil
}
