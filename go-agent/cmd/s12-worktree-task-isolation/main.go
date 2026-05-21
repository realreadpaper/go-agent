package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/tools"
	"learn-claude-code-go/internal/worktree"
)

func main() {
	// s12 把 task graph 和 git worktree 绑定起来。
	// task 描述“做什么”，worktree 描述“在哪里隔离地做”，所有变更事件写入 .worktrees/events.jsonl。
	loadNearestDotEnv()

	prompt, err := readPrompt(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	taskManager, worktreeManager, err := loadManagers(workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager error: %v\n", err)
		os.Exit(1)
	}
	if handled, err := handleCommand(prompt, taskManager, worktreeManager, os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
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
	fmt.Fprintf(os.Stderr, "[s12] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	tasks.Register(reg, taskManager)
	worktree.Register(reg, worktreeManager)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    systemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 60,
		Trace:     prefixedWriter{prefix: "[s12] ", w: os.Stderr},
	}
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

func loadManagers(workdir string) (*tasks.Manager, *worktree.Manager, error) {
	taskManager, err := tasks.LoadManager(workdir)
	if err != nil {
		return nil, nil, err
	}
	worktreeManager, err := worktree.NewManager(workdir, taskManager)
	if err != nil {
		return nil, nil, err
	}
	return taskManager, worktreeManager, nil
}

func handleCommand(prompt string, taskManager *tasks.Manager, worktreeManager *worktree.Manager, w io.Writer) (bool, error) {
	switch strings.TrimSpace(prompt) {
	case "/tasks":
		ready, err := taskManager.ListReady()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, map[string]any{"tasks": taskManager.List(), "ready": ready})
	case "/worktrees":
		return true, writeJSON(w, map[string]any{"worktrees": worktreeManager.List()})
	case "/events":
		events, err := worktreeManager.Events(50)
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, map[string]any{"events": events})
	default:
		return false, nil
	}
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
		return "", fmt.Errorf("usage: s12-worktree-task-isolation <prompt|/tasks|/worktrees|/events>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding agent running in %s.
Use task_create/task_list to inspect or create durable work.
Use worktree_create to create an isolated git worktree for a task.
Use worktree_run for commands that must execute inside the isolated worktree path.
Use worktree_keep when the user wants to preserve a worktree for review.
Use worktree_remove with complete_task=true only when the worktree work is done and the user wants the task completed.
Always mention worktree name, task id, branch, and event summary in your final answer.`, workdir)
}

func printResponse(w io.Writer, resp llm.Response) {
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			fmt.Fprintln(w, block.Text)
		}
	}
}

func writeJSON(w io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

type prefixedWriter struct {
	prefix string
	w      io.Writer
}

func (p prefixedWriter) Write(data []byte) (int, error) {
	text := string(data)
	lines := strings.SplitAfter(text, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if _, err := fmt.Fprint(p.w, p.prefix+line); err != nil {
			return 0, err
		}
	}
	return len(data), nil
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
