package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RegisterBash 注册最小 agent 必备的 bash 工具。
// bash 是教学 harness 的第一把“手”：模型不能直接执行 shell，只能请求 harness 用这个 handler 执行命令。
// workdir 固定为命令执行目录，避免模型因为当前进程启动位置不同而得到不可预测结果。
func RegisterBash(reg *Registry, workdir string) {
	reg.Register(Tool{
		Spec: Spec("bash", "Run a bash command in the current workspace.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to run.",
				},
			},
			"required": []string{"command"},
		}),
		Handler: func(input map[string]any) (string, error) {
			command, err := stringArg(input, "command")
			if err != nil {
				return "", err
			}
			if err := rejectDangerousCommand(command); err != nil {
				return "", err
			}
			// 每次 shell 调用都设置超时，防止模型运行阻塞命令导致整个 agent loop 卡住。
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			// 使用 bash -lc 是为了让模型可以使用常见 shell 语法，例如管道、重定向和内置命令。
			cmd := exec.CommandContext(ctx, "bash", "-lc", command)
			cmd.Dir = workdir
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
			// stdout 和 stderr 都返回给模型。很多调试信息只写在 stderr，如果丢掉会让模型看不见失败原因。
			output := stdout.String() + stderr.String()
			if ctx.Err() == context.DeadlineExceeded {
				return output, fmt.Errorf("command timed out after 120s")
			}
			if err != nil {
				return output, err
			}
			return output, nil
		},
	})
}

// rejectDangerousCommand 是教学版的粗粒度安全护栏。
// 它不能替代生产权限系统，但能拦住最明显的破坏性命令，后续文件工具还会补充路径沙箱。
func rejectDangerousCommand(command string) error {
	for _, fragment := range []string{"rm -rf /", "sudo", "shutdown", "reboot", "> /dev/"} {
		if strings.Contains(command, fragment) {
			return fmt.Errorf("dangerous command rejected: contains %q", fragment)
		}
	}
	return nil
}
