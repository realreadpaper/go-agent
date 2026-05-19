package tools

import (
	"fmt"

	"learn-claude-code-go/internal/llm"
)

// Handler 是一个本地工具的执行函数。
// 输入来自模型生成的 JSON 参数；输出会作为 tool_result 文本返回给模型。
type Handler func(input map[string]any) (string, error)

// Tool 把“给模型看的 schema”和“本地真正执行的 handler”绑在一起。
// 这层绑定非常关键：模型只能看到 Spec，不能直接触碰 Go 函数或系统资源。
type Tool struct {
	Spec    llm.ToolSpec
	Handler Handler
}

// Registry 是工具分发中心。
// 新增工具时只需要 Register 一个 Tool，agent loop 不需要增加 if/else 分支。
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Spec 用统一格式构造 llm.ToolSpec，避免每个工具重复填写字段。
func Spec(name, description string, inputSchema map[string]any) llm.ToolSpec {
	return llm.ToolSpec{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
	}
}

// Register 将工具加入 registry。相同名称会被覆盖，方便测试中替换工具实现。
func (r *Registry) Register(t Tool) {
	r.tools[t.Spec.Name] = t
}

// Specs 返回当前所有工具 schema，供 LLM 在下一轮请求中选择。
// 返回值故意不暴露 handler，保持“模型选择工具，harness 执行工具”的边界。
func (r *Registry) Specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, tool.Spec)
	}
	return specs
}

// Run 按名称执行工具，并把所有异常都格式化成模型可读的字符串。
// agent loop 需要稳定拿到 tool_result，因此这里不把 handler error 继续向上传播。
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

// stringArg 从模型参数中读取必填字符串。
// 工具 handler 统一使用这类小校验函数，可以把“模型参数不合法”转成清晰错误消息。
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

// intArg 读取可选整数参数。
// JSON 解码后的数字常见类型是 float64，工具层要兼容它，避免模型传入 limit 后类型断言失败。
func intArg(input map[string]any, name string, fallback int) int {
	value, ok := input[name]
	if !ok || value == nil {
		return fallback
	}
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return fallback
	}
}
