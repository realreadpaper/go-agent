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
	"learn-claude-code-go/internal/background"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s08 让慢命令不再阻塞整个 agent loop。
	// background_run 立即返回 id；命令完成后，BeforeCall hook 会把通知注入下一轮上下文。
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
	fmt.Fprintf(os.Stderr, "[s08] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	backgroundManager := background.NewManager(workdir)
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	background.Register(reg, backgroundManager)

	loop := &agent.Loop{
		Client:     client,
		Model:      cfg.Model,
		System:     systemPrompt(workdir),
		Tools:      reg,
		MaxTokens:  cfg.MaxTokens,
		MaxRounds:  50,
		BeforeCall: []agent.BeforeCallHook{backgroundManager.BeforeCall},
		Trace:      os.Stderr,
	}
	fmt.Fprintf(os.Stderr, "[s08] workdir=%s prompt=%q\n", workdir, prompt)
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
		return "", fmt.Errorf("usage: s08-background-tasks <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding agent running in %s.
Use background_run for slow commands such as sleeps, long tests, installs, or builds so the agent can continue working.
Use background_check with the returned task id to inspect running/completed/failed state.
When background results are injected in <background-results>, use them as real command results.
Prefer read_file, write_file, and edit_file for file operations.
Use bash only for quick commands that do not need background execution.
When finished, answer the user directly and mention relevant background task ids.`, workdir)
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
