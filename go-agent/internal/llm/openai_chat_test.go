package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIChatClientPostsChatRequestAndParsesToolCalls(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "read_file",
							"arguments": `{"path":"README.md"}`,
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	client := NewOpenAIChatClient(Config{Model: "chat-test", APIKey: "test-key", BaseURL: server.URL, MaxTokens: 99}, server.Client())
	resp, err := client.Create(Request{
		System: "system text",
		Messages: []Message{
			{Role: "user", Content: "read"},
			{Role: "user", Content: []ToolResult{{Type: "tool_result", ToolUseID: "call_prev", Content: "previous output"}}},
		},
		Tools: []ToolSpec{{Name: "read_file", Description: "Read file", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotRequest["model"] != "chat-test" {
		t.Fatalf("model = %v", gotRequest["model"])
	}
	if gotRequest["max_tokens"].(float64) != 99 {
		t.Fatalf("max_tokens = %v", gotRequest["max_tokens"])
	}
	messages := gotRequest["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" {
		t.Fatalf("first message = %#v", messages[0])
	}
	foundToolMessage := false
	for _, msg := range messages {
		m := msg.(map[string]any)
		if m["role"] == "tool" && m["tool_call_id"] == "call_prev" {
			foundToolMessage = true
		}
	}
	if !foundToolMessage {
		t.Fatalf("messages did not include role=tool message: %#v", messages)
	}
	if len(gotRequest["tools"].([]any)) != 1 {
		t.Fatalf("tools = %#v", gotRequest["tools"])
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q", resp.StopReason)
	}
	block := resp.Content[0]
	if block.ID != "call_1" || block.Name != "read_file" || block.Input["path"] != "README.md" {
		t.Fatalf("tool block = %#v", block)
	}
}

func TestOpenAIChatClientParsesTextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "done",
				},
			}},
		})
	}))
	defer server.Close()

	client := NewOpenAIChatClient(Config{Model: "chat-test", APIKey: "test-key", BaseURL: server.URL}, server.Client())
	resp, err := client.Create(Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q", resp.StopReason)
	}
	if resp.Content[0].Text != "done" {
		t.Fatalf("text = %q", resp.Content[0].Text)
	}
}

func TestOpenAIChatClientUsesDeepSeekChatEndpoint(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "done",
				},
			}},
		})
	}))
	defer server.Close()

	client := NewOpenAIChatClient(Config{
		Provider: ProviderDeepSeek,
		APIStyle: APIStyleOpenAIChat,
		Model:    "deepseek-v4-pro",
		APIKey:   "test-key",
		BaseURL:  server.URL,
	}, server.Client())

	_, err := client.Create(Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
}
