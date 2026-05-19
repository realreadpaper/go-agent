package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicCompatClientPostsMessagesRequestAndParsesText(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer deepseek-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "hello"}},
			"stop_reason": "end_turn",
		})
	}))
	defer server.Close()

	client := NewAnthropicCompatClient(Config{Model: "deepseek-v4-pro", APIKey: "deepseek-key", BaseURL: server.URL, MaxTokens: 77}, server.Client())
	resp, err := client.Create(Request{
		System:   "system text",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []ToolSpec{{Name: "read_file", Description: "Read file", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotRequest["system"] != "system text" {
		t.Fatalf("system = %v", gotRequest["system"])
	}
	if gotRequest["max_tokens"].(float64) != 77 {
		t.Fatalf("max_tokens = %v", gotRequest["max_tokens"])
	}
	if len(gotRequest["tools"].([]any)) != 1 {
		t.Fatalf("tools = %#v", gotRequest["tools"])
	}
	if resp.StopReason != "end_turn" || resp.Content[0].Text != "hello" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestAnthropicCompatClientMapsToolUseAndToolResult(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{
				"type":  "tool_use",
				"id":    "toolu_1",
				"name":  "read_file",
				"input": map[string]any{"path": "README.md"},
			}},
			"stop_reason": "tool_use",
		})
	}))
	defer server.Close()

	client := NewAnthropicCompatClient(Config{Model: "deepseek-v4-pro", APIKey: "deepseek-key", BaseURL: server.URL}, server.Client())
	resp, err := client.Create(Request{Messages: []Message{
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ID: "toolu_prev", Name: "read_file", Input: map[string]any{"path": "a.txt"}}}},
		{Role: "user", Content: []ToolResult{{Type: "tool_result", ToolUseID: "toolu_prev", Content: "file text"}}},
	}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	messages := gotRequest["messages"].([]any)
	foundToolResult := false
	for _, msg := range messages {
		content := msg.(map[string]any)["content"]
		parts, ok := content.([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			p := part.(map[string]any)
			if p["type"] == "tool_result" && p["tool_use_id"] == "toolu_prev" {
				foundToolResult = true
			}
		}
	}
	if !foundToolResult {
		t.Fatalf("messages did not include tool_result: %#v", messages)
	}

	if resp.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q", resp.StopReason)
	}
	block := resp.Content[0]
	if block.ID != "toolu_1" || block.Name != "read_file" || block.Input["path"] != "README.md" {
		t.Fatalf("tool block = %#v", block)
	}
}
