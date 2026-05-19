package llm

import (
	"encoding/json"
	"net/http"
)

type OpenAIChatClient struct {
	cfg        Config
	httpClient *http.Client
}

func NewOpenAIChatClient(cfg Config, httpClient *http.Client) *OpenAIChatClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAIChatClient{cfg: cfg, httpClient: httpClient}
}

type openAIChatRequest struct {
	Model     string           `json:"model"`
	Messages  []map[string]any `json:"messages"`
	Tools     []map[string]any `json:"tools,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
	Store     bool             `json:"store,omitempty"`
}

func (c *OpenAIChatClient) Create(req Request) (Response, error) {
	body := openAIChatRequest{
		Model:     firstNonEmpty(req.Model, c.cfg.Model),
		Messages:  toOpenAIChatMessages(req.System, req.Messages),
		Tools:     toOpenAIChatTools(req.Tools),
		MaxTokens: firstNonZero(req.MaxTokens, c.cfg.MaxTokens),
		Store:     c.cfg.Store,
	}

	raw, err := doJSON(c.httpClient, http.MethodPost, joinURL(c.cfg.BaseURL, c.endpointPath()), c.cfg.APIKey, nil, body)
	if err != nil {
		return Response{}, err
	}
	resp, err := parseOpenAIChat(raw)
	return withRawBody(resp, raw, c.cfg.TraceRawAPI), err
}

func (c *OpenAIChatClient) endpointPath() string {
	if c.cfg.Provider == ProviderDeepSeek {
		return "/chat/completions"
	}
	return "/v1/chat/completions"
}

func toOpenAIChatTools(specs []ToolSpec) []map[string]any {
	tools := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        spec.Name,
				"description": spec.Description,
				"parameters":  spec.InputSchema,
			},
		})
	}
	return tools
}

func toOpenAIChatMessages(system string, messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages)+1)
	if system != "" {
		out = append(out, map[string]any{"role": "system", "content": system})
	}
	for _, msg := range messages {
		switch content := msg.Content.(type) {
		case string:
			out = append(out, map[string]any{"role": msg.Role, "content": content})
		case []ToolResult:
			for _, result := range content {
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": result.ToolUseID,
					"content":      result.Content,
				})
			}
		case []ContentBlock:
			chatMsg := map[string]any{"role": msg.Role}
			text := ""
			toolCalls := make([]map[string]any, 0)
			for _, block := range content {
				switch block.Type {
				case "text":
					text += block.Text
				case "tool_use":
					args, _ := json.Marshal(block.Input)
					toolCalls = append(toolCalls, map[string]any{
						"id":   block.ID,
						"type": "function",
						"function": map[string]any{
							"name":      block.Name,
							"arguments": string(args),
						},
					})
				}
			}
			chatMsg["content"] = text
			if len(toolCalls) > 0 {
				chatMsg["tool_calls"] = toolCalls
			}
			out = append(out, chatMsg)
		}
	}
	return out
}

func parseOpenAIChat(raw []byte) (Response, error) {
	var envelope struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Response{}, err
	}
	resp := Response{StopReason: "end_turn"}
	if len(envelope.Choices) == 0 {
		return resp, nil
	}
	choice := envelope.Choices[0]
	for _, call := range choice.Message.ToolCalls {
		input, err := parseArguments(call.Function.Arguments)
		if err != nil {
			return Response{}, err
		}
		resp.Content = append(resp.Content, ContentBlock{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	if choice.FinishReason == "tool_calls" || len(choice.Message.ToolCalls) > 0 {
		resp.StopReason = "tool_use"
		return resp, nil
	}
	if choice.Message.Content != "" {
		resp.Content = append(resp.Content, ContentBlock{Type: "text", Text: choice.Message.Content})
	}
	return resp, nil
}
