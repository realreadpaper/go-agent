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
	"learn-claude-code-go/internal/todo"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s03 在 s02 的工具集合上增加 todo 工具和 nag hook。
	// 这一步展示的是 harness 如何约束模型“持续维护计划”，而不是替模型决定计划内容。
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
	fmt.Fprintf(os.Stderr, "[s03] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	// 真实运行时使用持久化 Manager：每次模型调用 TodoWrite，都会在 .goagent/todo/
	// 生成一份独立 JSON 快照。这样可以复盘 agent 为什么做某一步，也能确认计划没有被覆盖。
	todoManager := todo.NewPersistentManager(workdir)
	todo.Register(reg, todoManager)
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
	fmt.Fprintf(os.Stderr, "[s03] workdir=%s todo_store=%s prompt=%q\n", workdir, todoManager.StoreDir(), prompt)
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
		return "", fmt.Errorf("usage: s03-todo-write <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	// system prompt 负责把“何时用 todo”说清楚。
	// Manager 仍会做硬校验：状态只能是 pending/in_progress/completed，且只能有一个 in_progress。
	return fmt.Sprintf(`You are a coding agent running in %s.
For any multi-step task, call the todo tool before editing files or running commands.
Keep the todo list updated as work progresses.
The todo tool input must be {"items":[{"content":"...","status":"pending|in_progress|completed","activeForm":"..."}]}.
Only one todo may be in_progress at a time.
Prefer read_file, write_file, and edit_file for file operations.
Use bash only when a shell command is genuinely needed.
When finished, mark all todos completed and answer the user directly.`, workdir)
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
