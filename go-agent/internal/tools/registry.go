package tools

import (
	"fmt"

	"learn-claude-code-go/internal/llm"
)

type Handler func(input map[string]any) (string, error)

type Tool struct {
	Spec    llm.ToolSpec
	Handler Handler
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func Spec(name, description string, inputSchema map[string]any) llm.ToolSpec {
	return llm.ToolSpec{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
	}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Spec.Name] = t
}

func (r *Registry) Specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, tool.Spec)
	}
	return specs
}

func (r *Registry) Run(name string, input map[string]any) string {
	tool, ok := r.tools[name]
	if !ok {
		return "Error: unknown tool: " + name
	}
	out, err := tool.Handler(input)
	if err != nil {
		return "Error: " + err.Error()
	}
	if out == "" {
		return "(no output)"
	}
	if len(out) > 50000 {
		return out[:50000]
	}
	return out
}

func stringArg(input map[string]any, name string) (string, error) {
	value, ok := input[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return text, nil
}
