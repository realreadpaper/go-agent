package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

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
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-lc", command)
			cmd.Dir = workdir
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
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

func rejectDangerousCommand(command string) error {
	for _, fragment := range []string{"rm -rf /", "sudo", "shutdown", "reboot", "> /dev/"} {
		if strings.Contains(command, fragment) {
			return fmt.Errorf("dangerous command rejected: contains %q", fragment)
		}
	}
	return nil
}
