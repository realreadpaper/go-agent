# AGENTS.md

## 项目约定

- 本项目默认使用中文沟通、中文说明和中文提交信息；代码标识符、命令、API 名称保持英文原文。
- Go 教学项目位于 `go-agent/`，优先执行 `cd go-agent && go test -count=1 ./...` 与 `cd go-agent && go vet ./...` 验证。
- 面向教学读者实现 Go 代码时，需要补充清晰的中文注释。读者默认是第一次接触 agent harness，需要能通过注释理解“模型请求工具、Go harness 执行工具、结果回填给模型”的完整链路。
- 中文注释优先写在核心类型、核心函数、跨模块边界、工具执行、安全限制、provider 协议转换和不直观的状态流转处。注释要解释“为什么这样做”和“边界在哪里”，不要只把代码逐行翻译成中文。
- CLI 示例命令需要默认打印必要调试日志，至少能看到 provider/model、当前轮次、stop_reason、tool_use 和 tool_result 摘要；禁止打印完整 API key。
- 真实密钥只保存在 `go-agent/.env`，该文件已被忽略，禁止暂存或提交。
- 本仓库包含本地 skill：`skills/git-commit/SKILL.md`。当用户要求提交 Git 时，按该 skill 的流程执行。
- 不要使用 `git add .`；只暂存本次任务明确相关的文件。
