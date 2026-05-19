package agent

import (
	"fmt"

	"learn-claude-code-go/internal/llm"
)

const maxToolResultChars = 50000

type BeforeCallHook func(messages *[]llm.Message) error
type AfterToolHook func(name string)

type ToolRunner interface {
	Specs() []llm.ToolSpec
	Run(name string, input map[string]any) string
}

type Loop struct {
	Client     llm.Client
	Model      string
	System     string
	Tools      ToolRunner
	MaxTokens  int
	MaxRounds  int
	BeforeCall []BeforeCallHook
	AfterTool  []AfterToolHook
}

func (l *Loop) Run(messages []llm.Message) ([]llm.Message, llm.Response, error) {
	var resp llm.Response
	rounds := l.MaxRounds
	if rounds == 0 {
		rounds = 50
	}

	for i := 0; i < rounds; i++ {
		for _, hook := range l.BeforeCall {
			if err := hook(&messages); err != nil {
				return messages, resp, err
			}
		}

		var specs []llm.ToolSpec
		if l.Tools != nil {
			specs = l.Tools.Specs()
		}
		var err error
		resp, err = l.Client.Create(llm.Request{
			Model:     l.Model,
			System:    l.System,
			Messages:  messages,
			Tools:     specs,
			MaxTokens: l.MaxTokens,
		})
		if err != nil {
			return messages, resp, err
		}
		messages = append(messages, llm.Message{Role: "assistant", Content: resp.Content})
		if resp.StopReason != "tool_use" {
			return messages, resp, nil
		}

		results := make([]llm.ToolResult, 0)
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			output := "Error: no tools registered"
			if l.Tools != nil {
				output = l.Tools.Run(block.Name, block.Input)
			}
			if len(output) > maxToolResultChars {
				output = output[:maxToolResultChars]
			}
			for _, hook := range l.AfterTool {
				hook(block.Name)
			}
			results = append(results, llm.ToolResult{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   output,
			})
		}
		messages = append(messages, llm.Message{Role: "user", Content: results})
	}

	return messages, resp, fmt.Errorf("agent loop exceeded MaxRounds=%d", rounds)
}
