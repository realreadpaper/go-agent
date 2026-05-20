package compact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

type summaryClient struct {
	requests []llm.Request
	summary  string
}

func (c *summaryClient) Create(req llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, req)
	return llm.Response{
		StopReason: "end_turn",
		Content:    []llm.ContentBlock{{Type: "text", Text: c.summary}},
	}, nil
}

func TestMicroCompactReplacesOldToolResults(t *testing.T) {
	messages := toolConversation(6)
	manager := &Manager{KeepRecent: 3}

	manager.MicroCompact(&messages)

	results := collectToolResults(messages)
	if len(results) != 6 {
		t.Fatalf("tool result count = %d, want 6", len(results))
	}
	for i := 0; i < 3; i++ {
		want := "[Previous: used bash]"
		if results[i].Content != want {
			t.Fatalf("old result %d = %q, want %q", i, results[i].Content, want)
		}
	}
	for i := 3; i < 6; i++ {
		if !strings.Contains(results[i].Content, "full output") {
			t.Fatalf("recent result %d was compacted unexpectedly: %q", i, results[i].Content)
		}
	}
}

func TestEstimateTokensUsesJSONLengthOverFour(t *testing.T) {
	messages := []llm.Message{{Role: "user", Content: strings.Repeat("abcd", 25)}}

	got := EstimateTokens(messages)

	if got <= 0 {
		t.Fatalf("EstimateTokens() = %d, want positive estimate", got)
	}
}

func TestAutoCompactWritesTranscriptAndReturnsSummaryMessage(t *testing.T) {
	workdir := t.TempDir()
	client := &summaryClient{summary: "Created file and verified result."}
	manager := &Manager{
		Client:        client,
		Model:         "test-model",
		TokenLimit:    1,
		TranscriptDir: filepath.Join(workdir, ".transcripts"),
	}
	messages := []llm.Message{
		{Role: "user", Content: "create a file"},
		{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: strings.Repeat("tool output ", 20)}}},
	}

	if err := manager.AutoCompactIfNeeded(&messages); err != nil {
		t.Fatalf("AutoCompactIfNeeded returned error: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("message count after compact = %d, want 1", len(messages))
	}
	text, ok := messages[0].Content.(string)
	if !ok || !strings.Contains(text, "[Compressed]") || !strings.Contains(text, client.summary) {
		t.Fatalf("compressed message = %#v", messages[0].Content)
	}
	if len(client.requests) != 1 {
		t.Fatalf("summary request count = %d, want 1", len(client.requests))
	}
	entries, err := os.ReadDir(manager.TranscriptDir)
	if err != nil {
		t.Fatalf("ReadDir transcript returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("transcript files = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(manager.TranscriptDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile transcript returned error: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 2 {
		t.Fatalf("transcript line count = %d, want 2\n%s", lines, data)
	}
}

func TestManualCompactToolRequestsNextHookCompact(t *testing.T) {
	client := &summaryClient{summary: "Manual summary."}
	manager := &Manager{
		Client:        client,
		Model:         "test-model",
		TokenLimit:    1_000_000,
		TranscriptDir: filepath.Join(t.TempDir(), ".transcripts"),
	}
	reg := tools.NewRegistry()
	RegisterCompact(reg, manager)

	out := reg.Run("compact", map[string]any{})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("compact tool returned error: %q", out)
	}
	messages := []llm.Message{{Role: "user", Content: "keep this fact"}}
	if err := manager.AutoCompactIfNeeded(&messages); err != nil {
		t.Fatalf("AutoCompactIfNeeded returned error: %v", err)
	}
	if got := messages[0].Content.(string); !strings.Contains(got, "Manual summary.") {
		t.Fatalf("manual compact message = %q", got)
	}
}

func toolConversation(count int) []llm.Message {
	messages := make([]llm.Message, 0, count*2)
	for i := 0; i < count; i++ {
		id := "tool-" + string(rune('a'+i))
		messages = append(messages, llm.Message{
			Role: "assistant",
			Content: []llm.ContentBlock{{
				Type: "tool_use",
				ID:   id,
				Name: "bash",
			}},
		})
		messages = append(messages, llm.Message{
			Role: "user",
			Content: []llm.ToolResult{{
				Type:      "tool_result",
				ToolUseID: id,
				Content:   "full output " + strings.Repeat("x", 80),
			}},
		})
	}
	return messages
}

func collectToolResults(messages []llm.Message) []llm.ToolResult {
	var results []llm.ToolResult
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if toolResults, ok := msg.Content.([]llm.ToolResult); ok {
			results = append(results, toolResults...)
		}
	}
	return results
}
