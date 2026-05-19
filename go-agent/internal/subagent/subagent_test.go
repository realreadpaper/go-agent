package subagent

import (
	"strings"
	"testing"

	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
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

func TestRunnerStartsWithFreshMessagesAndReturnsFinalSummary(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{
		StopReason: "end_turn",
		Content: []llm.ContentBlock{
			{Type: "text", Text: "uses go test"},
			{Type: "text", Text: " and go vet"},
		},
	}}}
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, t.TempDir())
	runner := &Runner{
		Client:    client,
		Model:     "test-model",
		System:    "subagent system",
		Tools:     reg,
		MaxRounds: 3,
	}

	summary, err := runner.Run("inspect tests")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if summary != "uses go test and go vet" {
		t.Fatalf("summary = %q", summary)
	}
	if len(client.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "inspect tests" {
		t.Fatalf("subagent did not start from fresh user prompt: %#v", req.Messages)
	}
	if req.System != "subagent system" {
		t.Fatalf("system = %q", req.System)
	}
}

func TestRunnerChildToolsDoNotIncludeTask(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{StopReason: "end_turn", Content: []llm.ContentBlock{{Type: "text", Text: "done"}}}}}
	childTools := tools.NewRegistry()
	childTools.Register(tools.Tool{Spec: tools.Spec("read_file", "Read", map[string]any{"type": "object"}), Handler: func(map[string]any) (string, error) {
		return "ok", nil
	}})
	runner := &Runner{Client: client, Tools: childTools}

	_, err := runner.Run("child work")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	for _, spec := range client.requests[0].Tools {
		if spec.Name == "task" {
			t.Fatalf("child tools unexpectedly included task: %#v", client.requests[0].Tools)
		}
	}
}

type stubRunner struct {
	prompt string
}

func (r *stubRunner) Run(prompt string) (string, error) {
	r.prompt = prompt
	return "child summary", nil
}

func TestRegisterTaskRunsSubagentAndReturnsOnlySummary(t *testing.T) {
	reg := tools.NewRegistry()
	runner := &stubRunner{}
	RegisterTask(reg, runner)

	out := reg.Run("task", map[string]any{"prompt": "inspect repository"})
	if out != "child summary" {
		t.Fatalf("task output = %q", out)
	}
	if runner.prompt != "inspect repository" {
		t.Fatalf("runner prompt = %q", runner.prompt)
	}
}

func TestRegisterTaskRequiresPrompt(t *testing.T) {
	reg := tools.NewRegistry()
	RegisterTask(reg, &stubRunner{})

	out := reg.Run("task", map[string]any{"prompt": 42})
	if !strings.Contains(out, "prompt must be a string") {
		t.Fatalf("invalid prompt output = %q", out)
	}
}
