package agent

import (
	"strings"
	"testing"

	"learn-claude-code-go/internal/llm"
)

type scriptedClient struct {
	responses []llm.Response
	requests  []llm.Request
}

func (c *scriptedClient) Create(req llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return llm.Response{}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

type scriptedTools struct {
	outputs map[string]string
	calls   []string
}

func (t *scriptedTools) Specs() []llm.ToolSpec {
	return []llm.ToolSpec{{Name: "bash", Description: "Run bash", InputSchema: map[string]any{"type": "object"}}}
}

func (t *scriptedTools) Run(name string, input map[string]any) string {
	t.calls = append(t.calls, name)
	return t.outputs[name]
}

func TestLoopRunsToolAndAppendsToolResultBeforeFinalResponse(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{
			StopReason: "tool_use",
			Content: []llm.ContentBlock{{
				Type:  "tool_use",
				ID:    "tool-1",
				Name:  "bash",
				Input: map[string]any{"command": "echo hi"},
			}},
		},
		{
			StopReason: "end_turn",
			Content:    []llm.ContentBlock{{Type: "text", Text: "done"}},
		},
	}}
	tools := &scriptedTools{outputs: map[string]string{"bash": "hi\n"}}
	loop := &Loop{
		Client:    client,
		Model:     "test-model",
		System:    "system",
		Tools:     tools,
		MaxRounds: 4,
	}

	messages, resp, err := loop.Run([]llm.Message{{Role: "user", Content: "say hi"}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if resp.StopReason != "end_turn" {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if len(client.requests) != 2 {
		t.Fatalf("LLM calls = %d, want 2", len(client.requests))
	}
	if got := client.requests[0].Tools[0].Name; got != "bash" {
		t.Fatalf("tool spec name = %q, want bash", got)
	}
	if len(tools.calls) != 1 || tools.calls[0] != "bash" {
		t.Fatalf("tool calls = %#v, want [bash]", tools.calls)
	}

	lastUser := messages[2]
	results, ok := lastUser.Content.([]llm.ToolResult)
	if !ok {
		t.Fatalf("third message content type = %T, want []llm.ToolResult", lastUser.Content)
	}
	if len(results) != 1 {
		t.Fatalf("tool results = %d, want 1", len(results))
	}
	if results[0].ToolUseID != "tool-1" || results[0].Content != "hi\n" {
		t.Fatalf("tool result = %#v", results[0])
	}
}

func TestLoopHooksAndTruncatesLargeToolResults(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{
			StopReason: "tool_use",
			Content: []llm.ContentBlock{{
				Type:  "tool_use",
				ID:    "tool-1",
				Name:  "bash",
				Input: map[string]any{"command": "yes"},
			}},
		},
		{StopReason: "end_turn"},
	}}
	tools := &scriptedTools{outputs: map[string]string{"bash": strings.Repeat("x", maxToolResultChars+100)}}
	beforeCalls := 0
	afterTools := 0
	loop := &Loop{
		Client: client,
		Tools:  tools,
		BeforeCall: []BeforeCallHook{func(messages *[]llm.Message) error {
			beforeCalls++
			return nil
		}},
		AfterTool: []AfterToolHook{func(name string) {
			afterTools++
		}},
	}

	messages, _, err := loop.Run([]llm.Message{{Role: "user", Content: "run"}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if beforeCalls != 2 {
		t.Fatalf("BeforeCall count = %d, want 2", beforeCalls)
	}
	if afterTools != 1 {
		t.Fatalf("AfterTool count = %d, want 1", afterTools)
	}
	results := messages[2].Content.([]llm.ToolResult)
	if len(results[0].Content) != maxToolResultChars {
		t.Fatalf("tool result length = %d, want %d", len(results[0].Content), maxToolResultChars)
	}
}

func TestLoopReturnsErrorWhenMaxRoundsExceeded(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{
			StopReason: "tool_use",
			Content: []llm.ContentBlock{{
				Type: "tool_use",
				ID:   "tool-1",
				Name: "bash",
			}},
		},
	}}
	tools := &scriptedTools{outputs: map[string]string{"bash": "again"}}
	loop := &Loop{Client: client, Tools: tools, MaxRounds: 1}

	_, _, err := loop.Run([]llm.Message{{Role: "user", Content: "loop"}})
	if err == nil {
		t.Fatal("Run returned nil error after MaxRounds exceeded")
	}
}
