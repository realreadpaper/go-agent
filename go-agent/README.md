# Go Agent Harness

这是一个教学用 Go agent harness。它从最小 LLM loop 开始，逐步加入工具调用、TodoWrite、子 agent、skill loading、上下文压缩、持久任务图、后台任务、团队 inbox、request-response 协议和自主任务认领。

项目目标不是复刻某个生产级 agent 平台，而是把 agent 能力拆成可以读懂、可以测试、可以单独运行的小机制。第一次接触 agent 的读者可以按 `cmd/s01...cmd/sfull` 的顺序学习。

## 快速开始

```bash
cd go-agent
go test ./...
```

OpenAI GPT-5.5:

```bash
AGENT_LLM_PROVIDER=openai \
AGENT_LLM_API_STYLE=openai_responses \
AGENT_MODEL=gpt-5.5 \
OPENAI_API_KEY=... \
go run ./cmd/s01-agent-loop "List files"
```

DeepSeek V4 Flash:

```bash
AGENT_LLM_PROVIDER=deepseek \
AGENT_LLM_API_STYLE=openai_chat \
AGENT_MODEL=deepseek-v4-flash \
DEEPSEEK_API_KEY=... \
DEEPSEEK_BASE_URL=https://api.deepseek.com \
go run ./cmd/s01-agent-loop "List files"
```

DeepSeek V4 Pro:

```bash
AGENT_LLM_PROVIDER=deepseek \
AGENT_LLM_API_STYLE=anthropic_messages \
AGENT_MODEL=deepseek-v4-pro \
DEEPSEEK_API_KEY=... \
DEEPSEEK_BASE_URL=https://api.deepseek.com/anthropic \
go run ./cmd/s01-agent-loop "List files"
```

也可以把这些变量写到 `go-agent/.env`。CLI 会向上查找最近的 `.env` 并加载，但不会打印 API key。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `AGENT_LLM_PROVIDER` | `openai`、`deepseek` 或 `anthropic_compat` |
| `AGENT_LLM_API_STYLE` | `openai_responses`、`openai_chat` 或 `anthropic_messages` |
| `AGENT_MODEL` | 模型名，默认 OpenAI 为 `gpt-5.5`，DeepSeek 为 `deepseek-v4-flash` |
| `AGENT_REASONING_EFFORT` | OpenAI Responses 推理强度，默认 `medium` |
| `AGENT_MAX_TOKENS` | 最大输出 token，默认 `8000` |
| `AGENT_TRACE_RAW_API` | 是否把原始 API body 放入调试日志，默认 `false` |
| `OPENAI_API_KEY` | OpenAI API key |
| `OPENAI_BASE_URL` | OpenAI compatible base URL，默认 `https://api.openai.com` |
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `DEEPSEEK_BASE_URL` | DeepSeek base URL |

API style 建议：

- OpenAI 默认使用 Responses API：`AGENT_LLM_API_STYLE=openai_responses`
- DeepSeek V4 Flash 推荐 OpenAI-compatible Chat：`AGENT_LLM_API_STYLE=openai_chat`
- DeepSeek V4 Pro 可选 Anthropic-compatible：`AGENT_LLM_API_STYLE=anthropic_messages`

## 命令与章节映射

| 命令 | 文档章节 | 重点机制 |
| --- | --- | --- |
| `cmd/s01-agent-loop` | Task 2 | 最小 agent loop |
| `cmd/s02-tool-use` | Task 3 | 本地工具调用 |
| `cmd/s03-todo-write` | Task 4 | TodoWrite |
| `cmd/s04-subagent` | Task 5 | 一次性子 agent |
| `cmd/s05-skill-loading` | Task 6 | skill loading 和 skill generator |
| `cmd/s06-context-compact` | Task 7 | 上下文压缩 |
| `cmd/s07-task-system` | Task 8 | 持久任务图 |
| `cmd/s08-background-tasks` | Task 9 | 后台任务 |
| `cmd/s09-agent-teams` | Task 10 | 持久队友和 JSONL inbox |
| `cmd/s10-team-protocols` | Task 11 | request-response 协议 |
| `cmd/s11-autonomous-agents` | Task 12 | 自主 idle 和任务认领 |
| `cmd/s12-worktree-task-isolation` | Task 13 | git worktree 任务隔离 |
| `cmd/sfull` | Task 14 | 完整 harness 组装 |

## sfull

`cmd/sfull` 会注册当前已实现的完整能力：

- `bash`
- `read_file`、`write_file`、`edit_file`
- `todo`
- `task` 子 agent
- `load_skill`
- `compact`
- `task_create`、`task_update`、`task_get`、`task_list`
- `background_run`、`background_check`
- `spawn_teammate`、`list_teammates`、`send_message`、`broadcast`、`read_inbox`
- `shutdown_request`、`shutdown_response`、`plan_submit`、`plan_review`、`request_status`
- teammate 内部的 `idle` 和 `claim_task`
- `worktree_create`、`worktree_run`、`worktree_keep`、`worktree_remove`、`worktree_list`、`worktree_events`

单次 prompt:

```bash
go run ./cmd/sfull "Create tmp/full-demo.txt, read it back, create one task, then show the task list."
```

REPL:

```bash
go run ./cmd/sfull
```

REPL 命令：

- `/compact`: 请求下一轮压缩上下文
- `/tasks`: 输出 `.tasks` 中的任务和 ready 任务
- `/team`: 输出 `.team/config.json` 中的队友状态
- `/inbox`: 读取并清空 lead inbox
- `/worktrees`: 输出 `.worktrees/index.json` 中的 worktree 记录
- `/exit`: 退出

## Tool Calling 映射

内部统一使用：

- `llm.ContentBlock{Type:"tool_use"}`
- `llm.ToolResult{Type:"tool_result"}`

provider adapter 负责协议转换：

- OpenAI Responses: `function_call` 和 `function_call_output`
- OpenAI Chat: `tool_calls` 和 `tool` message
- Anthropic-compatible: `tool_use` 和 `tool_result`

模型只能请求工具。真正执行 shell、读写文件、修改任务和发送消息的动作都发生在 Go harness 的 `tools.Registry` handler 中。

## 本地状态目录

运行时会产生这些目录，默认不提交：

- `.goagent/todo/`: 当前运行的最终 TodoWrite 快照
- `.tasks/`: 持久任务图
- `.team/`: team config、inbox 和 protocol requests
- `.transcripts/`: context compact 前的 transcript
- `.worktrees/`: git worktree index、events 和隔离工作目录

## 故障排查

- `401`: API key 缺失或错误。检查 `.env` 和 provider 对应 key。
- `404`: base URL 或模型名不匹配。OpenAI compatible 服务通常需要确认是否带 `/v1`。
- `unsupported model`: 当前 provider 不支持该模型名，或 API style 选错。
- `tool schema rejected`: 工具 schema 写法不被 provider 接受。优先看 CLI stderr 中的请求信息。
- reasoning items 未回传: OpenAI Responses 的 reasoning 项需要由 adapter 维护上下文映射；不要手动改 transcript。
- DeepSeek unsupported field ignored: DeepSeek 兼容接口可能忽略部分 OpenAI 字段，优先使用 `openai_chat` 或文档推荐的兼容样式。
- agent 卡住慢命令: 使用 `background_run`，再用 `background_check` 查看结果。

## 安全边界

本项目不启用 OpenAI hosted tools。所有工具都是本地 harness tools，由 Go 代码显式注册和执行。

教学版 `bash` 工具有基础危险命令拦截和超时，但它不是生产沙箱。不要在不可信目录或含敏感文件的环境中让模型自由执行命令。

## 验证

```bash
go test -count=1 ./...
go test -race ./internal/background ./internal/team ./internal/autonomous
go vet ./...
```

真实模型 smoke:

```bash
go run ./cmd/sfull "Create tmp/full-demo.txt, read it back, create one task, then show /tasks."
```
