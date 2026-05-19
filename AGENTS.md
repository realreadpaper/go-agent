# AGENTS.md

## 项目约定

- 本项目默认使用中文沟通、中文说明和中文提交信息；代码标识符、命令、API 名称保持英文原文。
- Go 教学项目位于 `go-agent/`，优先执行 `cd go-agent && go test -count=1 ./...` 与 `cd go-agent && go vet ./...` 验证。
- 面向教学读者实现 Go 代码时，需要补充清晰的中文注释。注释重点解释 agent harness 的控制流、状态边界、工具安全边界和 provider 协议转换；代码标识符仍保持英文。
- 真实密钥只保存在 `go-agent/.env`，该文件已被忽略，禁止暂存或提交。
- 本仓库包含本地 skill：`skills/git-commit/SKILL.md`。当用户要求提交 Git 时，按该 skill 的流程执行。
- 不要使用 `git add .`；只暂存本次任务明确相关的文件。
