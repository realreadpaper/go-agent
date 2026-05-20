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
	"learn-claude-code-go/internal/compact"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s06 的目标是让长时间运行的 agent 不被历史工具输出拖垮。
	// 完整 transcript 会写入磁盘，活跃上下文则在需要时替换成摘要。
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
	fmt.Fprintf(os.Stderr, "[s06] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	transcriptDir := filepath.Join(workdir, ".transcripts")
	compactManager := &compact.Manager{
		Client:        client,
		Model:         cfg.Model,
		System:        compactSystemPrompt(workdir),
		MaxTokens:     cfg.MaxTokens,
		TokenLimit:    6_000,
		KeepRecent:    3,
		TranscriptDir: transcriptDir,
	}

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	compact.RegisterCompact(reg, compactManager)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    systemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 50,
		BeforeCall: []agent.BeforeCallHook{
			compactManager.MicroCompact,
			compactManager.AutoCompactIfNeeded,
		},
		Trace: os.Stderr,
	}
	fmt.Fprintf(os.Stderr, "[s06] workdir=%s transcript_dir=%s prompt=%q\n", workdir, transcriptDir, prompt)
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
		return "", fmt.Errorf("usage: s06-context-compact <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir string) string {
	return fmt.Sprintf(`You are a coding agent running in %s.
Use read_file, write_file, edit_file, and bash to complete tasks.
Use the compact tool when the conversation has accumulated enough detail and a shorter working memory would help.
After compacting, continue from the compressed summary and answer the user directly.`, workdir)
}

func compactSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You summarize an agent transcript from %s.
Preserve user goals, important files, commands already run, tool results that affect future work, decisions, blockers, and next steps.
Write a concise Chinese summary that lets the agent continue after context compaction.`, workdir)
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
