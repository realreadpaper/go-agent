package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIResponsesClientPostsResponsesRequestAndParsesText(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_1",
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{"type": "output_text", "text": "hello"},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAIResponsesClient(Config{
		Model:           "gpt-test",
		APIKey:          "test-key",
		BaseURL:         server.URL,
		MaxTokens:       123,
		ReasoningEffort: "low",
	}, server.Client())

	resp, err := client.Create(Request{
		System:   "You are helpful.",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []ToolSpec{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotRequest["model"] != "gpt-test" {
		t.Fatalf("model = %v", gotRequest["model"])
	}
	if gotRequest["instructions"] != "You are helpful." {
		t.Fatalf("instructions = %v", gotRequest["instructions"])
	}
	if gotRequest["store"] != false {
		t.Fatalf("store = %v, want false", gotRequest["store"])
	}
	if gotRequest["max_output_tokens"].(float64) != 123 {
		t.Fatalf("max_output_tokens = %v", gotRequest["max_output_tokens"])
	}
	if len(gotRequest["tools"].([]any)) != 1 {
		t.Fatalf("tools = %#v", gotRequest["tools"])
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if got := resp.Content[0].Text; got != "hello" {
		t.Fatalf("text = %q, want hello", got)
	}
}

func TestOpenAIResponsesClientParsesFunctionCallAsToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []any{
				map[string]any{
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "read_file",
					"arguments": `{"path":"README.md"}`,
				},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAIResponsesClient(Config{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL}, server.Client())

	resp, err := client.Create(Request{Messages: []Message{{Role: "user", Content: "read"}}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Fatalf("StopReason = %q, want tool_use", resp.StopReason)
	}
	block := resp.Content[0]
	if block.Type != "tool_use" || block.ID != "call_1" || block.Name != "read_file" {
		t.Fatalf("tool block = %#v", block)
	}
	if block.Input["path"] != "README.md" {
		t.Fatalf("path = %#v", block.Input["path"])
	}
}

func TestOpenAIResponsesClientStoresRawBodyOnlyWhenTraceRawAPIEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"output_text":"hello"}`))
	}))
	defer server.Close()

	withoutTrace := NewOpenAIResponsesClient(Config{
		Model:   "gpt-test",
		APIKey:  "test-key",
		BaseURL: server.URL,
	}, server.Client())
	resp, err := withoutTrace.Create(Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Create without trace returned error: %v", err)
	}
	if resp.RawBody != "" {
		t.Fatalf("RawBody without trace = %q, want empty", resp.RawBody)
	}

	withTrace := NewOpenAIResponsesClient(Config{
		Model:       "gpt-test",
		APIKey:      "test-key",
		BaseURL:     server.URL,
		TraceRawAPI: true,
	}, server.Client())
	resp, err = withTrace.Create(Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Create with trace returned error: %v", err)
	}
	if resp.RawBody != `{"output_text":"hello"}` {
		t.Fatalf("RawBody with trace = %q", resp.RawBody)
	}
}

func TestOpenAIResponsesClientSendsFunctionCallOutputForToolResults(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"output": []any{}})
	}))
	defer server.Close()

	client := NewOpenAIResponsesClient(Config{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL}, server.Client())
	_, err := client.Create(Request{Messages: []Message{
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ID: "call_1", Name: "read_file", Input: map[string]any{"path": "a.txt"}}}},
		{Role: "user", Content: []ToolResult{{Type: "tool_result", ToolUseID: "call_1", Content: "file text"}}},
	}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	input := gotRequest["input"].([]any)
	foundOutput := false
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "function_call_output" && m["call_id"] == "call_1" && m["output"] == "file text" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("input did not contain function_call_output: %#v", input)
	}
}

func TestOpenAIResponsesClientReturnsStatusAndBodyOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request body", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewOpenAIResponsesClient(Config{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL}, server.Client())

	_, err := client.Create(Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("Create returned nil error")
	}
}

func TestJoinURLDoesNotDuplicateV1(t *testing.T) {
	got := joinURL("https://example.test/v1", "/v1/responses")
	if got != "https://example.test/v1/responses" {
		t.Fatalf("joinURL duplicated version path: %q", got)
	}
}
