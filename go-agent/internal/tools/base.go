package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// SafePath 把模型传入的路径固定到 workdir 之内。
// 文件工具都必须先走这一层：模型可以提出路径，但不能越过 harness 给它划定的工作区边界。
func SafePath(workdir, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", requested)
	}
	return candidate, nil
}

// RegisterFileTools 注册 s02 引入的专用文件工具。
// 它们比让模型自己拼 shell 命令更可靠：参数可校验、路径可沙箱、输出格式也更稳定。
func RegisterFileTools(reg *Registry, workdir string) {
	reg.Register(Tool{
		Spec: Spec("read_file", "Read a text file from the current workspace.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Path to read, relative to the workspace."},
				"limit": map[string]any{"type": "integer", "description": "Optional maximum number of lines to return."},
			},
			"required": []string{"path"},
		}),
		Handler: func(input map[string]any) (string, error) {
			path, err := stringArg(input, "path")
			if err != nil {
				return "", err
			}
			safe, err := SafePath(workdir, path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(safe)
			if err != nil {
				return "", err
			}
			return limitLines(string(data), intArg(input, "limit", 0)), nil
		},
	})

	reg.Register(Tool{
		Spec: Spec("write_file", "Write a text file inside the current workspace.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to write, relative to the workspace."},
				"content": map[string]any{"type": "string", "description": "Complete file content to write."},
			},
			"required": []string{"path", "content"},
		}),
		Handler: func(input map[string]any) (string, error) {
			path, err := stringArg(input, "path")
			if err != nil {
				return "", err
			}
			content, err := stringArg(input, "content")
			if err != nil {
				return "", err
			}
			safe, err := SafePath(workdir, path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(safe), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(safe, []byte(content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %s (%d bytes)", path, len(content)), nil
		},
	})

	reg.Register(Tool{
		Spec: Spec("edit_file", "Replace the first matching text in a file inside the current workspace.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "description": "Path to edit, relative to the workspace."},
				"old_text": map[string]any{"type": "string", "description": "Existing text to replace. Only the first match is replaced."},
				"new_text": map[string]any{"type": "string", "description": "Replacement text."},
			},
			"required": []string{"path", "old_text", "new_text"},
		}),
		Handler: func(input map[string]any) (string, error) {
			path, err := stringArg(input, "path")
			if err != nil {
				return "", err
			}
			oldText, err := stringArg(input, "old_text")
			if err != nil {
				return "", err
			}
			newText, err := stringArg(input, "new_text")
			if err != nil {
				return "", err
			}
			safe, err := SafePath(workdir, path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(safe)
			if err != nil {
				return "", err
			}
			text := string(data)
			index := strings.Index(text, oldText)
			if index < 0 {
				return "", fmt.Errorf("text not found in %s", path)
			}
			updated := text[:index] + newText + text[index+len(oldText):]
			if err := os.WriteFile(safe, []byte(updated), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Edited %s", path), nil
		},
	})
}

func limitLines(text string, limit int) string {
	if limit <= 0 {
		return strings.TrimRight(text, "\n")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= limit {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:limit], "\n")
}
