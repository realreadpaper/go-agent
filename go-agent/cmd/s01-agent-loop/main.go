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
	// 教学 CLI 默认从当前目录向上寻找 .env。
	// 这样读者在 go-agent/ 或仓库根目录运行命令时，都能复用同一份本地密钥配置。
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
	// 调试日志刻意不打印 API key，只打印 provider/model/style/base_url。
	// 用户看到这些信息就能判断当前到底连的是 OpenAI、DeepSeek 还是兼容服务。
	fmt.Fprintf(os.Stderr, "[s01] provider=%s api_style=%s model=%s base_url=%s\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    systemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 50,
		Trace:     os.Stderr,
	}
	fmt.Fprintf(os.Stderr, "[s01] workdir=%s prompt=%q\n", workdir, prompt)
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

func readPrompt(args []string, stdin io.Reader) (string, error) {
	// 命令行参数优先；没有参数时再读 stdin，便于脚本和管道调用。
	if len(args) > 0 {
		return strings.TrimSpace(strings.Join(args, " ")), nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("usage: s01-agent-loop <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	// system prompt 说明 agent 的运行位置和唯一工具。
	// s01 的教学目标是让读者先看懂“一个 loop + 一个 bash 工具”的最小闭环。
	return fmt.Sprintf(`You are a coding agent running in %s.
You may use the bash tool to inspect files, run commands, and verify work.
When you have enough information, answer the user directly.`, workdir)
}

func printResponse(w io.Writer, resp llm.Response) {
	// 最终回答只写 stdout；调试日志写 stderr。
	// 这样用户既能观察执行过程，也能把最终答案通过管道交给其他命令。
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			fmt.Fprintln(w, block.Text)
		}
	}
}

func loadNearestDotEnv() {
	// 从当前目录逐级向上查找 .env，找到第一份就加载。
	// 真实 key 只留在本地 ignored 文件里，不能写进代码或提交历史。
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
		// 已存在的环境变量优先，避免 .env 覆盖用户临时指定的 provider/model。
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
