# Go Agent Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 基于 `/Users/hejianglong/Desktop/github/learn-claude-code/docs/zh` 的 s01-s12 文档，实现一个 Go 语言版本的 Claude Code 风格 agent harness 教学项目。

**Architecture:** Go 版本保留原文档的核心教学线索：一个稳定 agent loop，加上工具分发、Todo、Subagent、Skill、压缩、任务图、后台任务、团队邮箱、协议、自主认领和 worktree 隔离。实现上用共享 `internal/*` 包承载机制，用 `cmd/s01` 到 `cmd/s12` 暴露逐步演进示例，避免每节复制整套逻辑。

**Tech Stack:** Go 1.23+、standard library、direct HTTP LLM adapters、OpenAI Responses API、OpenAI-compatible Chat Completions API、DeepSeek Anthropic-compatible Messages API、DeepSeek OpenAI-compatible Chat API、`gopkg.in/yaml.v3`、`go test`、JSON/JSONL 持久化、`os/exec`、goroutine/channel、`git worktree`。

---

## 1. 文档到能力映射

| 文档 | Go 版本目标 | 关键机制 |
|---|---|---|
| `s01-the-agent-loop.md` | 最小 agent loop | messages 累积、LLM 调用、`stop_reason != tool_use` 退出、bash 工具 |
| `s02-tool-use.md` | 工具注册表 | `ToolRegistry`、路径沙箱、read/write/edit/bash |
| `s03-todo-write.md` | 会话内 Todo | 单一 `in_progress`、nag reminder、渲染状态 |
| `s04-subagent.md` | 一次性子 agent | 独立 messages、父上下文只接收摘要、禁止递归 task |
| `s05-skill-loading.md` | 按需 Skill | 扫描 `skills/**/SKILL.md`、frontmatter、`load_skill` |
| `s06-context-compact.md` | 三层上下文压缩 | micro compact、auto compact、manual compact、transcripts |
| `s07-task-system.md` | 持久任务图 | `.tasks/task_N.json`、`blockedBy`、状态转换、解锁依赖 |
| `s08-background-tasks.md` | 后台命令 | goroutine 执行、通知队列、状态查询 |
| `s09-agent-teams.md` | 持久队友 | `.team/config.json`、JSONL inbox、队友 goroutine loop |
| `s10-team-protocols.md` | 团队协议 | request/response FSM、shutdown、plan approval |
| `s11-autonomous-agents.md` | 自主队友 | WORK/IDLE、轮询收件箱和任务板、自动认领、身份重注入 |
| `s12-worktree-task-isolation.md` | 任务 worktree 隔离 | `.worktrees/index.json`、events.jsonl、任务与 worktree 绑定 |

## 2. 目标目录结构

在当前仓库创建：

```text
go-agent/
  go.mod
  go.sum
  README.md
  cmd/
    s01-agent-loop/main.go
    s02-tool-use/main.go
    s03-todo-write/main.go
    s04-subagent/main.go
    s05-skill-loading/main.go
    s06-context-compact/main.go
    s07-task-system/main.go
    s08-background-tasks/main.go
    s09-agent-teams/main.go
    s10-team-protocols/main.go
    s11-autonomous-agents/main.go
    s12-worktree-task-isolation/main.go
    sfull/main.go
  internal/
    agent/loop.go
    agent/options.go
    agent/repl.go
    llm/types.go
    llm/config.go
    llm/client_factory.go
    llm/openai_responses.go
    llm/openai_chat.go
    llm/anthropic_compat.go
    llm/deepseek.go
    llm/fake.go
    tools/registry.go
    tools/base.go
    tools/schema.go
    todo/manager.go
    subagent/subagent.go
    skills/loader.go
    compact/manager.go
    tasks/manager.go
    background/manager.go
    team/bus.go
    team/manager.go
    protocols/fsm.go
    autonomous/idle.go
    worktree/manager.go
    store/json.go
    runfiles/paths.go
  skills/
    agent-builder/SKILL.md
    code-review/SKILL.md
    mcp-builder/SKILL.md
    pdf/SKILL.md
```

职责边界：

- `internal/llm`: 只负责模型 API 抽象、多 provider HTTP adapter、请求/响应格式转换，不依赖 agent 业务。
- `internal/agent`: 只负责消息循环、hook、工具执行和 REPL。
- `internal/tools`: 注册工具 schema 与 handler，handler 返回字符串结果。
- `internal/* manager`: 各机制的状态、持久化、并发控制。
- `cmd/*`: 每节只组装本节需要的工具和 manager，不放核心业务逻辑。

## 3. 核心数据模型

`internal/llm/types.go` 定义统一 message/block/tool 结构：

```go
package llm

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ContentBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	Text  string         `json:"text,omitempty"`
}

type ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type Response struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

type Client interface {
	Create(req Request) (Response, error)
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
}
```

`internal/tools/registry.go` 定义工具注册：

```go
package tools

import "learn-claude-code-go/internal/llm"

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
	return out
}
```

`internal/agent/loop.go` 负责稳定循环：

```go
package agent

import "learn-claude-code-go/internal/llm"

type BeforeCallHook func(messages *[]llm.Message) error
type AfterToolHook func(name string)

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

type ToolRunner interface {
	Specs() []llm.ToolSpec
	Run(name string, input map[string]any) string
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
		var err error
		resp, err = l.Client.Create(llm.Request{
			Model: l.Model, System: l.System, Messages: messages,
			Tools: l.Tools.Specs(), MaxTokens: l.MaxTokens,
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
			output := l.Tools.Run(block.Name, block.Input)
			for _, hook := range l.AfterTool {
				hook(block.Name)
			}
			results = append(results, llm.ToolResult{Type: "tool_result", ToolUseID: block.ID, Content: output})
		}
		messages = append(messages, llm.Message{Role: "user", Content: results})
	}
	return messages, resp, nil
}
```

`internal/llm/config.go` 定义 provider 配置，CLI 和 README 都围绕这个模型展开：

```go
package llm

type Provider string

const (
	ProviderOpenAI          Provider = "openai"
	ProviderDeepSeek        Provider = "deepseek"
	ProviderAnthropicCompat Provider = "anthropic_compat"
)

type APIStyle string

const (
	APIStyleOpenAIResponses APIStyle = "openai_responses"
	APIStyleOpenAIChat      APIStyle = "openai_chat"
	APIStyleAnthropic       APIStyle = "anthropic_messages"
)

type Config struct {
	Provider        Provider
	APIStyle        APIStyle
	Model           string
	APIKey          string
	BaseURL         string
	MaxTokens       int
	ReasoningEffort string
	ThinkingEnabled bool
	Store           bool
}

func DefaultConfigFromEnv() (Config, error)
```

默认建议：

| Provider | 默认 model | 默认 API style | base URL | 说明 |
|---|---|---|---|---|
| `openai` | `gpt-5.5` | `openai_responses` | `https://api.openai.com` | 新项目优先使用 Responses API，保留 Chat adapter 做兼容测试 |
| `deepseek` | `deepseek-v4-pro` | `anthropic_messages` | `https://api.deepseek.com/anthropic` | 与本项目 `tool_use/tool_result` 循环最贴近 |
| `deepseek` | `deepseek-v4-flash` | `openai_chat` | `https://api.deepseek.com` | 低成本或兼容 OpenAI SDK 形态时使用 |

## 4. 多模型 API 接入方案

### 4.1 官方依据

- OpenAI 官方模型页显示 `gpt-5.5` 支持 `v1/responses` 和 `v1/chat/completions`，支持 function calling、structured outputs，并支持 `none/low/medium/high/xhigh` reasoning effort。实现中将 `openai_responses` 作为 OpenAI 默认路径。
- OpenAI 迁移指南说明 Responses API 是面向 agent-like 应用的统一接口，支持 built-in tools、自定义 function calling、多轮状态和 reasoning 模型更好的工具使用。实现中只接自定义 function tools，不使用 OpenAI hosted shell、apply patch、MCP 等托管工具，避免和本项目本地 harness 工具混淆。
- DeepSeek 官方 quick start 说明 API 同时兼容 OpenAI 和 Anthropic 格式，OpenAI base URL 是 `https://api.deepseek.com`，Anthropic base URL 是 `https://api.deepseek.com/anthropic`，V4 模型包括 `deepseek-v4-flash` 和 `deepseek-v4-pro`。
- DeepSeek Anthropic API 兼容文档说明 `tool_use`、`tool_result`、`tools.name`、`tools.input_schema`、`tools.description` 均受支持。Go 版本应优先通过 Anthropic-compatible adapter 接 DeepSeek V4，因为它能最小化 agent loop 的格式转换。

参考链接：
- OpenAI GPT-5.5 model: `https://developers.openai.com/api/docs/models/gpt-5.5`
- OpenAI Responses migration: `https://platform.openai.com/docs/guides/responses-vs-chat-completions?api-mode=responses`
- OpenAI Responses API reference: `https://platform.openai.com/docs/api-reference/responses/compact?api-mode=responses`
- OpenAI function calling: `https://platform.openai.com/docs/guides/function-calling/function-calling-with-structured-outputs?api-mode=responses`
- DeepSeek quick start: `https://api-docs.deepseek.com/`
- DeepSeek Anthropic API: `https://api-docs.deepseek.com/guides/anthropic_api`
- DeepSeek models and pricing: `https://api-docs.deepseek.com/quick_start/pricing/`

### 4.2 适配器职责

`llm.Client` 是唯一对外接口。不同供应商只在 adapter 内处理 wire format：

| Adapter | 文件 | 输入格式 | 输出归一化 |
|---|---|---|---|
| `OpenAIResponsesClient` | `internal/llm/openai_responses.go` | `POST /v1/responses`，`input` 为 item list，`tools` 为 function tools | 将 `function_call` 转为 `ContentBlock{Type:"tool_use"}`，将 message output 转为 `text` |
| `OpenAIChatClient` | `internal/llm/openai_chat.go` | `POST /v1/chat/completions`，`messages` + `tools` | 将 `tool_calls` 转为 `tool_use`，将 assistant content 转为 `text` |
| `AnthropicCompatClient` | `internal/llm/anthropic_compat.go` | `POST /v1/messages` 或 compatible base URL 下的 messages endpoint | 原生映射 `tool_use/tool_result` |
| `DeepSeekClient` | `internal/llm/deepseek.go` | 根据 `APIStyle` 包装 `OpenAIChatClient` 或 `AnthropicCompatClient` | 不直接实现协议，只设置 DeepSeek 默认 base URL、headers 和 model |

### 4.3 OpenAI Responses 格式转换

请求转换：

```go
type openAIResponsesRequest struct {
	Model     string         `json:"model"`
	Instructions string      `json:"instructions,omitempty"`
	Input     []map[string]any `json:"input"`
	Tools     []map[string]any `json:"tools,omitempty"`
	MaxOutputTokens int       `json:"max_output_tokens,omitempty"`
	Reasoning map[string]any `json:"reasoning,omitempty"`
	Store     bool           `json:"store"`
}
```

工具 schema 转换：

```go
func toOpenAIFunctionTools(specs []ToolSpec) []map[string]any {
	tools := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, map[string]any{
			"type": "function",
			"name": spec.Name,
			"description": spec.Description,
			"parameters": spec.InputSchema,
		})
	}
	return tools
}
```

响应转换规则：

- `output[].type == "function_call"` -> `ContentBlock{Type:"tool_use", ID: call_id, Name: name, Input: parsed arguments}`。
- `output[].type == "message"` 中的文本 -> `ContentBlock{Type:"text", Text: text}`。
- 只要存在 function call，统一返回 `StopReason: "tool_use"`。
- 对 reasoning items：保留在 provider transcript 中；当 OpenAI 返回需要回传的 reasoning item 时，下一轮 Responses input 必须包含上一轮 output item 和本地 `function_call_output` item。

### 4.4 OpenAI Chat Completions 格式转换

保留 Chat adapter 的目的：

- 兼容 DeepSeek OpenAI-style Chat API。
- 方便对比 Responses 与 Chat 的行为。
- 作为用户已有 OpenAI-compatible gateway 的降级路径。

转换规则：

- system prompt 转为首条 `role=system` message。
- `llm.Message{Role:"user", Content: []ToolResult}` 转为 `role=tool` messages；`tool_call_id` 使用 `ToolUseID`。
- assistant `tool_calls` 转为本项目 `tool_use`。
- `finish_reason == "tool_calls"` 时返回 `StopReason: "tool_use"`。

### 4.5 DeepSeek V4 接入策略

默认推荐：

```bash
export AGENT_LLM_PROVIDER=deepseek
export AGENT_LLM_API_STYLE=anthropic_messages
export AGENT_MODEL=deepseek-v4-pro
export DEEPSEEK_API_KEY=...
export DEEPSEEK_BASE_URL=https://api.deepseek.com/anthropic
```

低成本或高吞吐场景：

```bash
export AGENT_LLM_PROVIDER=deepseek
export AGENT_LLM_API_STYLE=openai_chat
export AGENT_MODEL=deepseek-v4-flash
export DEEPSEEK_API_KEY=...
export DEEPSEEK_BASE_URL=https://api.deepseek.com
```

注意：

- 不使用即将弃用的 `deepseek-chat`、`deepseek-reasoner` 作为默认值。
- `deepseek-v4-pro` 用于复杂编码和计划任务。
- `deepseek-v4-flash` 用于 smoke test、文档示例和成本敏感场景。
- Anthropic-compatible adapter 只使用 DeepSeek 明确支持的字段：`model`、`max_tokens`、`system`、`messages`、`tools`、`temperature`、`top_p`、`thinking`、`output_config.effort`。

### 4.6 统一环境变量

```bash
# Shared
AGENT_LLM_PROVIDER=openai|deepseek|anthropic_compat
AGENT_LLM_API_STYLE=openai_responses|openai_chat|anthropic_messages
AGENT_MODEL=gpt-5.5|deepseek-v4-pro|deepseek-v4-flash
AGENT_MAX_TOKENS=8000
AGENT_REASONING_EFFORT=medium
AGENT_STORE=false

# OpenAI
OPENAI_API_KEY=...
OPENAI_BASE_URL=https://api.openai.com

# DeepSeek
DEEPSEEK_API_KEY=...
DEEPSEEK_BASE_URL=https://api.deepseek.com/anthropic
```

配置优先级：

1. CLI flag，例如 `--provider deepseek --model deepseek-v4-pro`。
2. 环境变量。
3. provider 默认值。

### 4.7 文档补充范围

`go-agent/README.md` 必须包含：

- Provider 快速开始：OpenAI GPT-5.5、DeepSeek V4 Pro、DeepSeek V4 Flash。
- API style 选择建议：OpenAI 默认 Responses；DeepSeek 默认 Anthropic-compatible。
- Tool calling 映射说明：OpenAI `function_call` / Chat `tool_calls` / Anthropic `tool_use` 如何统一到内部 block。
- 故障排查：401、404、unsupported model、tool schema rejected、reasoning items 未回传、DeepSeek unsupported field ignored。
- 安全声明：OpenAI hosted tools 不启用，本项目只执行本地 harness tools。

## 5. 任务拆解

### Task 1: 初始化 Go 项目与 LLM 抽象

**Files:**
- Create: `go-agent/go.mod`
- Create: `go-agent/internal/llm/types.go`
- Create: `go-agent/internal/llm/config.go`
- Create: `go-agent/internal/llm/client_factory.go`
- Create: `go-agent/internal/llm/openai_responses.go`
- Create: `go-agent/internal/llm/openai_chat.go`
- Create: `go-agent/internal/llm/anthropic_compat.go`
- Create: `go-agent/internal/llm/deepseek.go`
- Create: `go-agent/internal/llm/fake.go`
- Test: `go-agent/internal/llm/fake_test.go`
- Test: `go-agent/internal/llm/config_test.go`
- Test: `go-agent/internal/llm/openai_responses_test.go`
- Test: `go-agent/internal/llm/openai_chat_test.go`
- Test: `go-agent/internal/llm/anthropic_compat_test.go`

- [ ] **Step 1: 初始化模块**

Run:

```bash
cd go-agent
go mod init learn-claude-code-go
go get gopkg.in/yaml.v3@latest
```

Expected: `go.mod` 存在，module 为 `learn-claude-code-go`。

- [ ] **Step 2: 写 `llm` 类型、配置解析与 fake client 测试**

测试覆盖：
- `FakeClient` 按顺序返回预设响应；响应耗尽时返回错误。
- `DefaultConfigFromEnv()` 可解析 OpenAI GPT-5.5：

```bash
AGENT_LLM_PROVIDER=openai
AGENT_LLM_API_STYLE=openai_responses
AGENT_MODEL=gpt-5.5
OPENAI_API_KEY=test-key
```

- `DefaultConfigFromEnv()` 可解析 DeepSeek V4 Pro：

```bash
AGENT_LLM_PROVIDER=deepseek
AGENT_LLM_API_STYLE=anthropic_messages
AGENT_MODEL=deepseek-v4-pro
DEEPSEEK_API_KEY=test-key
```

- 缺少 provider 对应 API key 时返回清晰错误。

Run:

```bash
cd go-agent
go test ./internal/llm
```

Expected: PASS。

- [ ] **Step 3: 实现 OpenAI Responses adapter**

要求：
- 从 `OPENAI_API_KEY` 读取 key。
- `OPENAI_BASE_URL` 可覆盖，默认 `https://api.openai.com`。
- 请求 `POST <base>/v1/responses`。
- `ToolSpec` 转为 OpenAI function tool。
- `ContentBlock{Type:"tool_use"}` 转为 Responses API 续轮输入。
- 工具执行结果转为 `function_call_output`，并绑定正确 `call_id`。
- `reasoning.effort` 从 `AGENT_REASONING_EFFORT` 读取，默认 `medium`。
- `store` 默认 `false`，避免教学 CLI 无意保存响应。
- 失败响应返回包含 status code 和 body 的 error。

- [ ] **Step 4: 实现 OpenAI Chat adapter**

要求：
- 请求 `POST <base>/v1/chat/completions`。
- system prompt 转为首条 `role=system`。
- `ToolSpec` 转为 Chat Completions `tools: [{type:"function", function:{...}}]`。
- `finish_reason == "tool_calls"` 映射为 `StopReason: "tool_use"`。
- `tool_calls[].function.arguments` 必须用 `encoding/json` 解析为 `map[string]any`。

- [ ] **Step 5: 实现 Anthropic-compatible adapter**

要求：
- 支持标准 Anthropic endpoint 或兼容 endpoint。
- DeepSeek 默认 base URL 使用 `https://api.deepseek.com/anthropic`。
- 请求 `/v1/messages`。
- 默认 header 包含兼容 Anthropic 的 `anthropic-version: 2023-06-01`。
- `tool_use/tool_result` 与内部模型一一映射。

- [ ] **Step 6: 实现 DeepSeek provider wrapper**

要求：
- `Provider=deepseek` 且 `APIStyle=anthropic_messages` 时创建 `AnthropicCompatClient`。
- `Provider=deepseek` 且 `APIStyle=openai_chat` 时创建 `OpenAIChatClient`。
- 默认模型为 `deepseek-v4-pro`。
- 默认 API style 为 `anthropic_messages`。
- `deepseek-v4-flash` 作为 README 中的低成本示例，不作为默认值。

- [ ] **Step 7: 实现 client factory**

```go
func NewClient(cfg Config, httpClient *http.Client) (Client, error)
```

规则：
- `openai + openai_responses` -> `OpenAIResponsesClient`。
- `openai + openai_chat` -> `OpenAIChatClient`。
- `deepseek + anthropic_messages` -> `DeepSeekClient` 包装 `AnthropicCompatClient`。
- `deepseek + openai_chat` -> `DeepSeekClient` 包装 `OpenAIChatClient`。
- 不支持的 provider/style 组合返回错误，错误中列出允许组合。

- [ ] **Step 8: 运行 LLM 层测试**

Run:

```bash
cd go-agent
go test ./internal/llm
```

Expected: PASS。测试使用 `httptest.Server`，不得访问真实网络。

- [ ] **Step 9: 提交**

```bash
git add go-agent/go.mod go-agent/go.sum go-agent/internal/llm
git commit -m "feat(go): add multi-provider llm clients"
```

### Task 2: 实现 s01 agent loop 和 bash 工具

**Files:**
- Create: `go-agent/internal/agent/loop.go`
- Create: `go-agent/internal/tools/registry.go`
- Create: `go-agent/internal/tools/base.go`
- Create: `go-agent/cmd/s01-agent-loop/main.go`
- Test: `go-agent/internal/agent/loop_test.go`
- Test: `go-agent/internal/tools/base_test.go`

- [ ] **Step 1: 写 loop 测试**

场景：
- fake client 第一轮返回 `tool_use`，工具结果被追加为 user message。
- fake client 第二轮返回文本，loop 退出。
- `MaxRounds` 防止无限工具调用。

Run:

```bash
cd go-agent
go test ./internal/agent
```

Expected: 先 FAIL，提示 `Loop` 未定义。

- [ ] **Step 2: 实现 loop 与 registry**

按第 3 节代码实现 `Loop.Run` 和 `Registry`。工具结果内容截断到 50,000 字符。

- [ ] **Step 3: 实现 bash 工具**

`internal/tools/base.go`：
- `RegisterBash(reg, workdir string)`。
- 使用 `exec.CommandContext(ctx, "bash", "-lc", command)`。
- timeout 120 秒。
- 拦截危险片段：`rm -rf /`、`sudo`、`shutdown`、`reboot`、`> /dev/`。

- [ ] **Step 4: 实现 s01 CLI**

`cmd/s01-agent-loop/main.go`：
- 从命令行参数或 stdin 读取 prompt。
- 调用 `llm.DefaultConfigFromEnv()` 和 `llm.NewClient()`，构造 provider-specific client、`tools.Registry`、`agent.Loop`。
- 注册仅 `bash`。
- system prompt 说明当前 workdir 和 bash 工具。

Run:

```bash
cd go-agent
go test ./...
go run ./cmd/s01-agent-loop "What is the current git branch?"
```

Expected: 测试 PASS；运行命令时如未配置 API key，打印清晰错误。

- [ ] **Step 5: 提交**

```bash
git add go-agent/internal/agent go-agent/internal/tools go-agent/cmd/s01-agent-loop
git commit -m "feat(go): implement s01 agent loop"
```

### Task 3: 实现 s02 工具分发、文件工具和路径沙箱

**Files:**
- Modify: `go-agent/internal/tools/base.go`
- Create: `go-agent/cmd/s02-tool-use/main.go`
- Test: `go-agent/internal/tools/base_test.go`

- [ ] **Step 1: 写路径沙箱测试**

覆盖：
- `read_file` 可读 workdir 内文件。
- `../outside.txt` 返回 path escapes workspace。
- `write_file` 自动创建父目录。
- `edit_file` 只替换第一次出现的 `old_text`。

- [ ] **Step 2: 实现 `SafePath` 与文件工具**

Go 实现点：
- `filepath.Abs` + `filepath.Rel` 判断路径没有 `..` 前缀。
- `read_file` 支持 `limit` 行数。
- 文件读写使用 `os.ReadFile`、`os.WriteFile`。
- `edit_file` 找不到文本时返回 `Error: text not found in <path>`。

- [ ] **Step 3: 实现 s02 CLI**

注册 `bash`、`read_file`、`write_file`、`edit_file`。

Run:

```bash
cd go-agent
go test ./internal/tools
go run ./cmd/s02-tool-use "Create a file called greet.go with a greet function, then read it"
```

Expected: 测试 PASS；CLI 可让模型调用文件工具。

- [ ] **Step 4: 提交**

```bash
git add go-agent/internal/tools go-agent/cmd/s02-tool-use
git commit -m "feat(go): add file tools and dispatch"
```

### Task 4: 实现 s03 TodoWrite 和 nag reminder

**Files:**
- Create: `go-agent/internal/todo/manager.go`
- Modify: `go-agent/internal/agent/options.go`
- Create: `go-agent/cmd/s03-todo-write/main.go`
- Test: `go-agent/internal/todo/manager_test.go`

- [ ] **Step 1: 写 Todo 测试**

覆盖：
- 多个 `in_progress` 返回错误。
- 非法状态返回错误。
- 空 `content` 返回错误。
- `Render()` 输出 `[ ]`、`[>]`、`[x]`。

- [ ] **Step 2: 实现 `TodoManager`**

字段：

```go
type Item struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}
```

`Update(items []Item) (string, error)` 校验状态并保存；最多 20 条。

- [ ] **Step 3: 注册 `todo` 工具和 nag hook**

实现：
- `todo.Register(reg, manager)`。
- `agent.WithTodoNag(manager, threshold int)` 返回 `BeforeCallHook` 和 `AfterToolHook` 所需状态。
- 当连续 3 轮未调用 `todo`，向最新 user tool_result message 前插入 `<reminder>Update your todos.</reminder>`。

- [ ] **Step 4: 实现 s03 CLI**

system prompt 明确多步任务先调用 `todo`，同一时间只有一个 `in_progress`。

Run:

```bash
cd go-agent
go test ./internal/todo ./internal/agent
go run ./cmd/s03-todo-write "Refactor hello.go: add type hints equivalent comments, doc comments, and tests"
```

Expected: 测试 PASS；nag hook 单测可验证 reminder 注入。

- [ ] **Step 5: 提交**

```bash
git add go-agent/internal/todo go-agent/internal/agent go-agent/cmd/s03-todo-write
git commit -m "feat(go): add todo planning tool"
```

### Task 5: 实现 s04 Subagent

**Files:**
- Create: `go-agent/internal/subagent/subagent.go`
- Create: `go-agent/cmd/s04-subagent/main.go`
- Test: `go-agent/internal/subagent/subagent_test.go`

- [ ] **Step 1: 写 subagent 测试**

用 fake client 验证：
- 子 agent 使用全新的 messages。
- 父 agent 只收到最终摘要字符串。
- 子 agent 工具集中没有 `task`，防止递归。

- [ ] **Step 2: 实现 `Runner`**

```go
type Runner struct {
	Client llm.Client
	Model string
	System string
	Tools *tools.Registry
	MaxRounds int
}

func (r *Runner) Run(prompt string) (string, error)
```

摘要提取逻辑：拼接最终 response 中所有 `text` block；为空返回 `(no summary)`。

- [ ] **Step 3: 注册父端 `task` 工具**

`RegisterTask(reg, runner)`，input schema:

```json
{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}
```

- [ ] **Step 4: 实现 s04 CLI**

父端工具包含 base tools + `task`；子端工具仅包含 base tools。

Run:

```bash
cd go-agent
go test ./internal/subagent
go run ./cmd/s04-subagent "Use a subtask to find what testing framework this project uses"
```

Expected: 测试 PASS；父上下文只记录 task 的摘要结果。

- [ ] **Step 5: 提交**

```bash
git add go-agent/internal/subagent go-agent/cmd/s04-subagent
git commit -m "feat(go): add subagent runner"
```

### Task 6: 实现 s05 Skill 加载

**Files:**
- Create: `go-agent/internal/skills/loader.go`
- Create: `go-agent/cmd/s05-skill-loading/main.go`
- Copy/Create: `go-agent/skills/*/SKILL.md`
- Test: `go-agent/internal/skills/loader_test.go`

- [ ] **Step 1: 复制示例 skills**

从 `/Users/hejianglong/Desktop/github/learn-claude-code/skills` 复制到 `go-agent/skills`，保持目录名不变。

- [ ] **Step 2: 写 loader 测试**

覆盖：
- 递归扫描 `SKILL.md`。
- YAML frontmatter 中 `name` 优先于目录名。
- `Descriptions()` 只输出名称和 description。
- `Load("missing")` 返回 unknown skill 错误文本。

- [ ] **Step 3: 实现 loader**

使用 `gopkg.in/yaml.v3` 解析 frontmatter。`Load(name)` 返回：

```xml
<skill name="code-review">
...
</skill>
```

- [ ] **Step 4: 注册 `load_skill` 工具**

system prompt 包含：

```text
Skills available:
  - agent-builder: ...
  - code-review: ...
```

完整 Skill 内容只通过 tool result 注入。

Run:

```bash
cd go-agent
go test ./internal/skills
go run ./cmd/s05-skill-loading "What skills are available?"
```

Expected: 测试 PASS；CLI 能列出可用 skill。

- [ ] **Step 5: 提交**

```bash
git add go-agent/internal/skills go-agent/skills go-agent/cmd/s05-skill-loading go-agent/go.mod go-agent/go.sum
git commit -m "feat(go): add on-demand skill loading"
```

### Task 7: 实现 s06 上下文压缩

**Files:**
- Create: `go-agent/internal/compact/manager.go`
- Create: `go-agent/cmd/s06-context-compact/main.go`
- Test: `go-agent/internal/compact/manager_test.go`

- [ ] **Step 1: 写 micro compact 测试**

构造 6 个 tool_result，`KeepRecent=3`。执行后前 3 个长结果变为 `[Previous: used <tool>]`，最近 3 个保持原文。

- [ ] **Step 2: 实现 token 估算**

`EstimateTokens(messages []llm.Message) int`：将 JSON 长度除以 4，作为教学实现的近似值。

- [ ] **Step 3: 实现 auto compact**

`Manager.AutoCompact(messages)`：
- 保存完整 JSONL 到 `.transcripts/transcript_<unix>.jsonl`。
- 调用 `llm.Client` 生成摘要。
- 返回单条 user message：`[Compressed]\n\n<summary>`。

- [ ] **Step 4: 注册 manual `compact` 工具**

因为工具 handler 需要修改 loop 外部状态，用 `Manager.RequestManual()` 设置标记；下一轮工具执行后由 hook 替换 messages。

- [ ] **Step 5: 实现 s06 CLI**

BeforeCall 顺序：
1. `MicroCompact`
2. `AutoCompactIfNeeded`

Run:

```bash
cd go-agent
go test ./internal/compact
go run ./cmd/s06-context-compact "Use the compact tool to manually compress the conversation"
```

Expected: 测试 PASS；`.transcripts/` 写入 JSONL。

- [ ] **Step 6: 提交**

```bash
git add go-agent/internal/compact go-agent/cmd/s06-context-compact
git commit -m "feat(go): add context compaction"
```

### Task 8: 实现 s07 持久任务图

**Files:**
- Create: `go-agent/internal/tasks/manager.go`
- Create: `go-agent/cmd/s07-task-system/main.go`
- Test: `go-agent/internal/tasks/manager_test.go`

- [ ] **Step 1: 写任务图测试**

覆盖：
- `Create` 生成递增 ID。
- `Update(id, completed)` 自动从其他任务 `blockedBy` 移除该 ID。
- `ListReady()` 只返回 `pending`、无 owner、`blockedBy` 为空的任务。
- 状态仅允许 `pending`、`in_progress`、`completed`。

- [ ] **Step 2: 实现任务模型**

```go
type Task struct {
	ID          int    `json:"id"`
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"`
	BlockedBy   []int  `json:"blockedBy"`
	Owner       string `json:"owner"`
	Worktree    string `json:"worktree"`
}
```

每个任务保存为 `.tasks/task_<id>.json`。

- [ ] **Step 3: 注册工具**

工具：
- `task_create(subject, description)`
- `task_update(task_id, status, add_blocked_by, remove_blocked_by, owner, worktree)`
- `task_get(task_id)`
- `task_list()`

- [ ] **Step 4: 实现 s07 CLI**

保留 s03 Todo，但 system prompt 说明：复杂、多轮、跨 agent 目标优先使用持久 task graph。

Run:

```bash
cd go-agent
go test ./internal/tasks
go run ./cmd/s07-task-system "Create 3 tasks: setup, write code, write tests. Make them depend on each other."
```

Expected: `.tasks/task_1.json` 等文件存在；依赖完成后自动解锁。

- [ ] **Step 5: 提交**

```bash
git add go-agent/internal/tasks go-agent/cmd/s07-task-system
git commit -m "feat(go): add persistent task graph"
```

### Task 9: 实现 s08 后台任务

**Files:**
- Create: `go-agent/internal/background/manager.go`
- Create: `go-agent/cmd/s08-background-tasks/main.go`
- Test: `go-agent/internal/background/manager_test.go`

- [ ] **Step 1: 写后台任务测试**

覆盖：
- `Run("echo done")` 立即返回 task id。
- 完成后 `DrainNotifications()` 返回结果并清空队列。
- `Check(id)` 返回 `running` 或 `completed`。

- [ ] **Step 2: 实现 manager**

用 goroutine + mutex + buffered channel。命令执行：
- `exec.CommandContext(ctx, "bash", "-lc", command)`。
- timeout 300 秒。
- stdout + stderr 截断 50,000 字符；通知摘要截断 500 字符。

- [ ] **Step 3: 注册工具和 before hook**

工具：
- `background_run(command)`
- `background_check(task_id)`

BeforeCall hook：将 drain 出的通知追加为：

```xml
<background-results>
[bg:abcd1234] done
</background-results>
```

- [ ] **Step 4: 实现 s08 CLI**

Run:

```bash
cd go-agent
go test ./internal/background
go run ./cmd/s08-background-tasks "Run 'sleep 5 && echo done' in the background, then create a file while it runs"
```

Expected: 模型可在后台命令运行期间继续调用其他工具。

- [ ] **Step 5: 提交**

```bash
git add go-agent/internal/background go-agent/cmd/s08-background-tasks
git commit -m "feat(go): add background command runner"
```

### Task 10: 实现 s09 团队邮箱与持久队友

**Files:**
- Create: `go-agent/internal/team/bus.go`
- Create: `go-agent/internal/team/manager.go`
- Create: `go-agent/cmd/s09-agent-teams/main.go`
- Test: `go-agent/internal/team/bus_test.go`
- Test: `go-agent/internal/team/manager_test.go`

- [ ] **Step 1: 写 MessageBus 测试**

覆盖：
- `Send` append JSON line 到 `.team/inbox/<to>.jsonl`。
- `ReadInbox(name)` 返回全部消息并清空文件。
- 空 inbox 返回 `[]`。

- [ ] **Step 2: 实现 team config**

`.team/config.json`：

```json
{"members":[{"name":"alice","role":"coder","status":"working"}]}
```

manager 内存中维护 goroutine cancel 函数和状态锁。

- [ ] **Step 3: 实现队友 loop**

每个队友：
- 独立 messages。
- 每次 LLM 调用前读自己的 inbox。
- 结束时状态改为 `idle`。

- [ ] **Step 4: 注册团队工具**

工具：
- `spawn_teammate(name, role, prompt)`
- `list_teammates()`
- `send_message(to, content)`
- `broadcast(content)`
- `read_inbox(name)`

- [ ] **Step 5: 实现 s09 CLI**

支持 REPL 命令：
- `/team` 输出 config。
- `/inbox` drain lead inbox。

Run:

```bash
cd go-agent
go test ./internal/team
go run ./cmd/s09-agent-teams "Spawn alice as coder and bob as tester. Have alice send bob a message."
```

Expected: `.team/config.json` 和 `.team/inbox/*.jsonl` 可观察。

- [ ] **Step 6: 提交**

```bash
git add go-agent/internal/team go-agent/cmd/s09-agent-teams
git commit -m "feat(go): add persistent agent teams"
```

### Task 11: 实现 s10 request-response 团队协议

**Files:**
- Create: `go-agent/internal/protocols/fsm.go`
- Modify: `go-agent/internal/team/manager.go`
- Create: `go-agent/cmd/s10-team-protocols/main.go`
- Test: `go-agent/internal/protocols/fsm_test.go`

- [ ] **Step 1: 写 FSM 测试**

覆盖：
- 新请求状态为 `pending`。
- approve 后状态为 `approved`。
- reject 后状态为 `rejected`。
- unknown request id 返回错误。

- [ ] **Step 2: 实现通用请求模型**

```go
type Request struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	From    string         `json:"from"`
	To      string         `json:"to"`
	Status  string         `json:"status"`
	Payload map[string]any `json:"payload"`
}
```

支持 kind:
- `shutdown`
- `plan_approval`

- [ ] **Step 3: 注册协议工具**

工具：
- `shutdown_request(teammate)`
- `shutdown_response(request_id, approve, reason)`
- `plan_submit(plan)`
- `plan_review(request_id, approve, feedback)`

所有请求/响应通过 MessageBus 传递，并在 tracker 中更新状态。

- [ ] **Step 4: 实现 graceful shutdown**

队友收到 approved shutdown 后：
- 完成当前工具轮。
- 发送响应。
- 状态改为 `shutdown`。
- goroutine 返回。

- [ ] **Step 5: 实现 s10 CLI**

Run:

```bash
cd go-agent
go test ./internal/protocols ./internal/team
go run ./cmd/s10-team-protocols "Spawn alice as coder, then request her shutdown."
```

Expected: shutdown request 有 request_id，状态从 pending 到 approved/rejected。

- [ ] **Step 6: 提交**

```bash
git add go-agent/internal/protocols go-agent/internal/team go-agent/cmd/s10-team-protocols
git commit -m "feat(go): add team request protocols"
```

### Task 12: 实现 s11 自主队友

**Files:**
- Create: `go-agent/internal/autonomous/idle.go`
- Modify: `go-agent/internal/team/manager.go`
- Modify: `go-agent/internal/tasks/manager.go`
- Create: `go-agent/cmd/s11-autonomous-agents/main.go`
- Test: `go-agent/internal/autonomous/idle_test.go`

- [ ] **Step 1: 写任务认领测试**

覆盖：
- 只认领 `pending`、`owner == ""`、`blockedBy == []` 的任务。
- 认领后 task status 变为 `in_progress`，owner 写入队友名。
- 已阻塞任务不被认领。

- [ ] **Step 2: 实现 IDLE phase**

```go
type IdleConfig struct {
	PollInterval time.Duration
	Timeout      time.Duration
}
```

流程：
1. 每 5 秒读 inbox。
2. inbox 非空，注入 `<inbox>...</inbox>` 并回 WORK。
3. 扫描 ready unclaimed task。
4. 找到后 claim，并注入 `<auto-claimed>Task #N: subject</auto-claimed>`。
5. 60 秒无事可做则 shutdown。

- [ ] **Step 3: 实现 `idle` 和 `claim_task` 工具**

`idle` 工具让模型主动进入空闲阶段。`claim_task(task_id)` 手动认领任务，复用 TaskManager 校验。

- [ ] **Step 4: 实现身份重注入**

当队友 messages 长度小于等于 3 时，在开头插入：

```xml
<identity>You are 'alice', role: coder, team: default. Continue your work.</identity>
```

- [ ] **Step 5: 实现 s11 CLI**

支持 `/tasks` 和 `/team`。

Run:

```bash
cd go-agent
go test ./internal/autonomous ./internal/tasks ./internal/team
go run ./cmd/s11-autonomous-agents "Create 3 tasks on the board, then spawn alice and bob. Watch them auto-claim."
```

Expected: 队友能从 `.tasks/` 自动认领任务。

- [ ] **Step 6: 提交**

```bash
git add go-agent/internal/autonomous go-agent/internal/team go-agent/internal/tasks go-agent/cmd/s11-autonomous-agents
git commit -m "feat(go): add autonomous task claiming"
```

### Task 13: 实现 s12 worktree 任务隔离

**Files:**
- Create: `go-agent/internal/worktree/manager.go`
- Create: `go-agent/cmd/s12-worktree-task-isolation/main.go`
- Test: `go-agent/internal/worktree/manager_test.go`

- [ ] **Step 1: 写 worktree manager 测试**

使用临时 git 仓库：
- `Create("auth-refactor", taskID)` 调用 `git worktree add -b wt/auth-refactor .worktrees/auth-refactor HEAD`。
- `.worktrees/index.json` 写入 worktree 记录。
- task 写入 `worktree`，pending 变为 `in_progress`。
- `Remove(name, completeTask=true)` 完成任务、解绑 worktree、写 events。

- [ ] **Step 2: 实现数据模型**

```go
type Worktree struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	TaskID int    `json:"task_id"`
	Status string `json:"status"`
}
```

`.worktrees/index.json`：

```json
{"worktrees":[{"name":"auth-refactor","path":".worktrees/auth-refactor","branch":"wt/auth-refactor","task_id":1,"status":"active"}]}
```

- [ ] **Step 3: 实现事件流**

追加到 `.worktrees/events.jsonl`：
- `worktree.create.before`
- `worktree.create.after`
- `worktree.create.failed`
- `worktree.remove.before`
- `worktree.remove.after`
- `worktree.remove.failed`
- `worktree.keep`
- `task.completed`

- [ ] **Step 4: 注册 worktree 工具**

工具：
- `worktree_create(name, task_id)`
- `worktree_run(name, command)`
- `worktree_keep(name)`
- `worktree_remove(name, force, complete_task)`
- `worktree_list()`
- `worktree_events(limit)`

`worktree_run` 的 cwd 必须是 worktree path，不是主仓库。

- [ ] **Step 5: 实现 s12 CLI**

Run:

```bash
cd go-agent
go test ./internal/worktree ./internal/tasks
go run ./cmd/s12-worktree-task-isolation "Create worktree auth-refactor for task 1, then run git status in it."
```

Expected: 任务与 worktree 双向绑定；remove 可同时完成任务并移除 worktree。

- [ ] **Step 6: 提交**

```bash
git add go-agent/internal/worktree go-agent/cmd/s12-worktree-task-isolation
git commit -m "feat(go): add worktree task isolation"
```

### Task 14: 实现 capstone `sfull` 与 README

**Files:**
- Create: `go-agent/cmd/sfull/main.go`
- Create: `go-agent/README.md`
- Test: `go-agent/internal/integration/smoke_test.go`

- [ ] **Step 1: 组装 sfull**

`sfull` 包含：
- base tools
- todo
- task subagent
- load_skill
- compact
- task graph
- background
- team
- protocols
- autonomous

s12 worktree 工具可在 sfull 中默认启用，但 README 要说明需要在 git 仓库内运行。

- [ ] **Step 2: 实现 REPL 命令**

支持：
- `/compact`
- `/tasks`
- `/team`
- `/inbox`
- `/worktrees`
- `/exit`

- [ ] **Step 3: 编写 README**

内容：
- 环境变量：`AGENT_LLM_PROVIDER`、`AGENT_LLM_API_STYLE`、`AGENT_MODEL`、`AGENT_REASONING_EFFORT`、`OPENAI_API_KEY`、`OPENAI_BASE_URL`、`DEEPSEEK_API_KEY`、`DEEPSEEK_BASE_URL`。
- 每个 cmd 对应文档章节。
- OpenAI GPT-5.5 快速开始：

```bash
cd go-agent
go test ./...
AGENT_LLM_PROVIDER=openai \
AGENT_LLM_API_STYLE=openai_responses \
AGENT_MODEL=gpt-5.5 \
OPENAI_API_KEY=... \
go run ./cmd/s01-agent-loop "List files"
```

- DeepSeek V4 Pro 快速开始：

```bash
cd go-agent
AGENT_LLM_PROVIDER=deepseek \
AGENT_LLM_API_STYLE=anthropic_messages \
AGENT_MODEL=deepseek-v4-pro \
DEEPSEEK_API_KEY=... \
DEEPSEEK_BASE_URL=https://api.deepseek.com/anthropic \
go run ./cmd/s01-agent-loop "List files"
```

- DeepSeek V4 Flash 快速开始：

```bash
cd go-agent
AGENT_LLM_PROVIDER=deepseek \
AGENT_LLM_API_STYLE=openai_chat \
AGENT_MODEL=deepseek-v4-flash \
DEEPSEEK_API_KEY=... \
DEEPSEEK_BASE_URL=https://api.deepseek.com \
go run ./cmd/s01-agent-loop "List files"
```

- [ ] **Step 4: smoke tests**

Smoke tests 不调用真实 API，使用 fake client 覆盖：
- 所有 cmd 包可编译。
- s01 loop 可执行 fake 工具调用。
- s07-s12 的文件状态机可在 temp dir 下运行。

Run:

```bash
cd go-agent
go test ./...
go test -race ./internal/background ./internal/team
go vet ./...
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add go-agent/cmd/sfull go-agent/README.md go-agent/internal/integration
git commit -m "feat(go): add full harness demo"
```

## 6. 实现顺序和里程碑

1. **Milestone A: 单 agent 可用**
   - 完成 Task 1-4。
   - 验收：s01-s03 可运行，工具分发和 Todo 单测通过。

2. **Milestone B: 上下文和知识管理**
   - 完成 Task 5-7。
   - 验收：Subagent、Skill、Compact 均可 fake 测试；真实 CLI 有清晰错误路径。

3. **Milestone C: 持久任务和并发**
   - 完成 Task 8-9。
   - 验收：`.tasks/`、后台通知队列稳定，race test 通过。

4. **Milestone D: 团队协作和自治**
   - 完成 Task 10-12。
   - 验收：JSONL inbox、request FSM、自动认领任务可在 temp dir 测试。

5. **Milestone E: worktree 隔离和整合**
   - 完成 Task 13-14。
   - 验收：临时 git 仓库 worktree 测试通过，`sfull` 编译通过。

## 7. 测试策略

- 单元测试优先使用 fake LLM，不依赖网络和真实 API key。
- 文件状态机测试全部使用 `t.TempDir()`。
- worktree 测试创建临时 git repo：

```bash
git init
git config user.email test@example.com
git config user.name test
echo hello > README.md
git add README.md
git commit -m init
```

- 并发 manager 跑 race：

```bash
cd go-agent
go test -race ./internal/background ./internal/team
```

- 每个里程碑必须运行：

```bash
cd go-agent
go test ./...
go vet ./...
```

## 8. 关键设计约束

- Agent loop 保持稳定：新增机制通过 tool registry、before hook、after hook、manager 注入，不改 loop 主流程。
- 所有工具 handler 返回字符串，不直接写 stdout。
- 所有路径工具必须使用 workdir 沙箱。
- 所有可恢复状态写磁盘：`.tasks/`、`.team/`、`.transcripts/`、`.worktrees/`。
- 所有并发状态必须由 mutex/channel 保护。
- LLM API 隔离在 `internal/llm`，业务测试不访问网络。
- OpenAI 默认走 Responses API；DeepSeek V4 默认走 Anthropic-compatible Messages API。
- Provider adapter 只做协议转换，不执行工具、不读取业务状态。
- 教学项目不实现完整生产权限系统，只保留危险命令拦截和路径沙箱。

## 9. 风险与处理

- **OpenAI Responses API 续轮状态风险:** adapter 必须保存并回传上一轮 output item 和本地 `function_call_output`；用 `httptest.Server` 单测验证请求序列。
- **OpenAI Chat 与 Responses 工具格式差异风险:** 两个 adapter 分文件实现，共用内部 `ContentBlock`，禁止在 agent loop 中写 provider 分支。
- **DeepSeek 兼容字段差异风险:** DeepSeek adapter 只发送官方兼容字段；对不支持的 style/model 组合在启动时失败。
- **并发队友写同一文件风险:** s12 前如共享 workdir，README 明确这是教学限制；s12 用 worktree 隔离。
- **JSONL 并发写风险:** MessageBus 写入使用 per-bus mutex；后续可扩展 file lock。
- **上下文压缩丢信息风险:** auto compact 前完整 transcript 落盘；摘要只替换活跃上下文。
- **真实 LLM 行为不可测风险:** 单测验证 harness 状态机，CLI 手动验证模型协作效果。

## 10. 最终验收标准

- `go test ./...` 通过。
- `go vet ./...` 通过。
- `go test -race ./internal/background ./internal/team` 通过。
- `cmd/s01-agent-loop` 到 `cmd/s12-worktree-task-isolation` 全部可编译。
- README 能把每个 Go cmd 映射回中文文档 s01-s12。
- `internal/llm` 覆盖 OpenAI Responses、OpenAI Chat、DeepSeek Anthropic-compatible、DeepSeek OpenAI-compatible 的无网络 adapter tests。
- 在配置 OpenAI GPT-5.5 后，至少以下命令能完成一次真实交互：

```bash
cd go-agent
AGENT_LLM_PROVIDER=openai AGENT_LLM_API_STYLE=openai_responses AGENT_MODEL=gpt-5.5 OPENAI_API_KEY=... \
go run ./cmd/s01-agent-loop "What is the current git branch?"
```

- 在配置 DeepSeek V4 Pro 后，至少以下命令能完成一次真实交互：

```bash
cd go-agent
AGENT_LLM_PROVIDER=deepseek AGENT_LLM_API_STYLE=anthropic_messages AGENT_MODEL=deepseek-v4-pro DEEPSEEK_API_KEY=... \
go run ./cmd/s01-agent-loop "What is the current git branch?"
go run ./cmd/s05-skill-loading "What skills are available?"
go run ./cmd/s07-task-system "Create tasks for parse, transform, emit, and test."
```

## 11. 自检

- Spec coverage: s01-s12 每节都有对应 task、cmd、manager 或工具注册。
- Placeholder scan: 计划中没有未展开的占位说明。
- Type consistency: `Task`、`Worktree`、`ToolSpec`、`Message`、`Request` 命名在各任务中保持一致。
- 测试闭环: 每个机制至少有一个无网络单元测试；真实模型调用只用于手动验收。
