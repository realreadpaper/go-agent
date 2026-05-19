# AGENTS.md

## 项目约定

- 本项目默认使用中文沟通、中文说明和中文提交信息；代码标识符、命令、API 名称保持英文原文。
- Go 教学项目位于 `go-agent/`，优先执行 `cd go-agent && go test -count=1 ./...` 与 `cd go-agent && go vet ./...` 验证。
- 真实密钥只保存在 `go-agent/.env`，该文件已被忽略，禁止暂存或提交。
- 本仓库包含本地 skill：`skills/git-commit/SKILL.md`。当用户要求提交 Git 时，按该 skill 的流程执行。
- 不要使用 `git add .`；只暂存本次任务明确相关的文件。
