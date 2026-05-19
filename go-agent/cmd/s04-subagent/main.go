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
	"learn-claude-code-go/internal/subagent"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s04 的重点是上下文隔离：父 agent 通过 task 工具派发局部任务，
	// 子 agent 自己读文件和跑命令，最后只把摘要作为 tool_result 交回父上下文。
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
	fmt.Fprintf(os.Stderr, "[s04] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}

	childTools := tools.NewRegistry()
	tools.RegisterBash(childTools, workdir)
	tools.RegisterFileTools(childTools, workdir)
	childRunner := &subagent.Runner{
		Client:    client,
		Model:     cfg.Model,
		System:    subagentSystemPrompt(workdir),
		Tools:     childTools,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 30,
		Trace:     prefixedWriter{prefix: "[subagent] ", w: os.Stderr},
	}

	parentTools := tools.NewRegistry()
	tools.RegisterBash(parentTools, workdir)
	tools.RegisterFileTools(parentTools, workdir)
	subagent.RegisterTask(parentTools, childRunner)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    parentSystemPrompt(workdir),
		Tools:     parentTools,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 50,
		Trace:     prefixedWriter{prefix: "[parent] ", w: os.Stderr},
	}
	fmt.Fprintf(os.Stderr, "[s04] workdir=%s prompt=%q\n", workdir, prompt)
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
		return "", fmt.Errorf("usage: s04-subagent <prompt>")
	}
	return prompt, nil
}

func parentSystemPrompt(workdir string) string {
	// 父 agent 负责主线决策。遇到需要大量探索但只需要结论的问题，应优先用 task。
	return fmt.Sprintf(`You are a coding agent running in %s.
Use the task tool for focused repository inspection or other work that would otherwise add lots of temporary context.
The task tool returns only a final summary from a subagent.
Use read_file, write_file, edit_file, and bash directly only when the parent context genuinely needs the details.
When finished, answer the user directly.`, workdir)
}

func subagentSystemPrompt(workdir string) string {
	// 子 agent 没有 task 工具，避免递归派发。
	// 它可以自由探索，但最终回答应该是给父 agent 的短摘要。
	return fmt.Sprintf(`You are a focused subagent running in %s.
Use your local tools to complete the delegated task.
Return a concise final summary with the important findings or actions.
Do not include a full transcript of every command unless it is essential.`, workdir)
}

func printResponse(w io.Writer, resp llm.Response) {
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			fmt.Fprintln(w, block.Text)
		}
	}
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
