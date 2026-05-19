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
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s02 仍然是教学 CLI：先自动加载本地 .env，再把用户 prompt 交给 agent loop。
	// 和 s01 的区别只在工具集合变大，loop 本身不需要知道多了哪些工具。
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
	fmt.Fprintf(os.Stderr, "[s02] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    systemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 50,
		Trace:     os.Stderr,
	}
	fmt.Fprintf(os.Stderr, "[s02] workdir=%s prompt=%q\n", workdir, prompt)
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

func readPrompt(args []string, stdin io.Reader) (string, error) {
	// 命令行参数适合直接演示；stdin 适合把长 prompt 从文件或管道传进来。
	if len(args) > 0 {
		return strings.TrimSpace(strings.Join(args, " ")), nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("usage: s02-tool-use <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	// 明确鼓励模型优先使用专用文件工具。
	// bash 仍然保留给测试、git、复杂 shell 查询；普通文件读写应走受沙箱保护的工具。
	return fmt.Sprintf(`You are a coding agent running in %s.
Prefer read_file, write_file, and edit_file for file operations.
Use bash only when a shell command is genuinely needed.
All file paths must stay inside the current workspace.
When you have enough information, answer the user directly.`, workdir)
}

func printResponse(w io.Writer, resp llm.Response) {
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			fmt.Fprintln(w, block.Text)
		}
	}
}

func loadNearestDotEnv() {
	// 逐级向上查找 .env，保持和 s01 一致的本地配置体验。
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
