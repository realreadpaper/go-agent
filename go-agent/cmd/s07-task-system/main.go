package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/todo"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s07 把短期 TodoWrite 扩展成长期 task graph。
	// Todo 仍用于当前回合计划；Task 会写入 .tasks，适合跨会话、跨 agent 的持久工作。
	loadNearestDotEnv()

	prompt, err := readPrompt(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	cfg, err := llm.DefaultConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM config error: %v\n", err)
		os.Exit(1)
	}
	client, err := llm.NewClient(cfg, http.DefaultClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM client error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[s07] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	taskManager, err := tasks.LoadManager(workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task manager error: %v\n", err)
		os.Exit(1)
	}

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	todoManager := todo.NewPersistentManager(workdir)
	todo.Register(reg, todoManager)
	tasks.Register(reg, taskManager)
	nag := agent.WithTodoNag(3)

	loop := &agent.Loop{
		Client:     client,
		Model:      cfg.Model,
		System:     systemPrompt(workdir),
		Tools:      reg,
		MaxTokens:  cfg.MaxTokens,
		MaxRounds:  50,
		BeforeCall: []agent.BeforeCallHook{nag.BeforeCall},
		AfterTool:  []agent.AfterToolHook{nag.AfterTool},
		Trace:      os.Stderr,
	}
	fmt.Fprintf(os.Stderr, "[s07] workdir=%s task_dir=%s todo_store=%s prompt=%q\n", workdir, taskManager.Dir(), todoManager.StoreDir(), prompt)
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

func readPrompt(args []string, stdin io.Reader) (string, error) {
	if len(args) > 0 {
		return strings.TrimSpace(strings.Join(args, " ")), nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("usage: s07-task-system <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding agent running in %s.
Use todo for short-lived planning inside the current conversation.
For complex, multi-round, cross-agent, or restart-safe work, use the persistent task graph tools.
Create durable tasks with task_create, change dependencies or status with task_update, inspect one task with task_get, and inspect all/ready tasks with task_list.
Ready tasks are pending tasks with no owner and no blockedBy dependencies.
When marking a task completed, dependent tasks are automatically unblocked by the Go harness.
Prefer read_file, write_file, and edit_file for file operations.
Use bash only when a shell command is genuinely needed.
When finished, answer the user directly and summarize task ids and ready tasks.`, workdir)
}

func printResponse(w io.Writer, resp llm.Response) {
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			fmt.Fprintln(w, block.Text)
		}
	}
}

func loadNearestDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			loadDotEnv(path)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
