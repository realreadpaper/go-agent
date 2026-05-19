package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAIResponsesClient struct {
	cfg        Config
	httpClient *http.Client
}

func NewOpenAIResponsesClient(cfg Config, httpClient *http.Client) *OpenAIResponsesClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAIResponsesClient{cfg: cfg, httpClient: httpClient}
}

type openAIResponsesRequest struct {
	Model           string           `json:"model"`
	Instructions    string           `json:"instructions,omitempty"`
	Input           []map[string]any `json:"input"`
	Tools           []map[string]any `json:"tools,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Reasoning       map[string]any   `json:"reasoning,omitempty"`
	Store           bool             `json:"store"`
}

func (c *OpenAIResponsesClient) Create(req Request) (Response, error) {
	body := openAIResponsesRequest{
		Model:           firstNonEmpty(req.Model, c.cfg.Model),
		Instructions:    req.System,
		Input:           toOpenAIResponsesInput(req.Messages),
		Tools:           toOpenAIResponsesTools(req.Tools),
		MaxOutputTokens: firstNonZero(req.MaxTokens, c.cfg.MaxTokens),
		Store:           c.cfg.Store,
	}
	if c.cfg.ReasoningEffort != "" {
		body.Reasoning = map[string]any{"effort": c.cfg.ReasoningEffort}
	}

	raw, err := doJSON(c.httpClient, http.MethodPost, joinURL(c.cfg.BaseURL, "/v1/responses"), c.cfg.APIKey, nil, body)
	if err != nil {
		return Response{}, err
	}
	resp, err := parseOpenAIResponses(raw)
	return withRawBody(resp, raw, c.cfg.TraceRawAPI), err
}

func toOpenAIResponsesTools(specs []ToolSpec) []map[string]any {
	tools := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        spec.Name,
			"description": spec.Description,
			"parameters":  spec.InputSchema,
		})
	}
	return tools
}

func toOpenAIResponsesInput(messages []Message) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch content := msg.Content.(type) {
		case string:
			input = append(input, map[string]any{"role": msg.Role, "content": content})
		case []ToolResult:
			for _, result := range content {
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": result.ToolUseID,
					"output":  result.Content,
				})
			}
		case []ContentBlock:
			texts := make([]map[string]any, 0)
			for _, block := range content {
				switch block.Type {
				case "text":
					texts = append(texts, map[string]any{"type": "output_text", "text": block.Text})
				case "tool_use":
					args, _ := json.Marshal(block.Input)
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   block.ID,
						"name":      block.Name,
						"arguments": string(args),
					})
				}
			}
			if len(texts) > 0 {
				input = append(input, map[string]any{"role": msg.Role, "content": texts})
			}
		}
	}
	return input
}

func parseOpenAIResponses(raw []byte) (Response, error) {
	var envelope struct {
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Response{}, err
	}

	resp := Response{StopReason: "end_turn"}
	if envelope.OutputText != "" {
		resp.Content = append(resp.Content, ContentBlock{Type: "text", Text: envelope.OutputText})
	}
	for _, item := range envelope.Output {
		switch item.Type {
		case "function_call":
			input, err := parseArguments(item.Arguments)
			if err != nil {
				return Response{}, err
			}
			resp.Content = append(resp.Content, ContentBlock{
				Type:  "tool_use",
				ID:    item.CallID,
				Name:  item.Name,
				Input: input,
			})
			resp.StopReason = "tool_use"
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					resp.Content = append(resp.Content, ContentBlock{Type: "text", Text: part.Text})
				}
			}
		}
	}
	return resp, nil
}

func doJSON(client *http.Client, method, url, apiKey string, headers map[string]string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s failed: status %d: %s", method, url, httpResp.StatusCode, string(raw))
	}
	return raw, nil
}

func parseArguments(raw any) (map[string]any, error) {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return map[string]any{}, nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(value), &out); err != nil {
			return nil, err
		}
		return out, nil
	case map[string]any:
		return value, nil
	case nil:
		return map[string]any{}, nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func withRawBody(resp Response, raw []byte, enabled bool) Response {
	if enabled {
		resp.RawBody = string(raw)
	}
	return resp
}
