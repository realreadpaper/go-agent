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
	"learn-claude-code-go/internal/skills"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s05 展示“按需加载知识”：system prompt 只放 skill 名称和描述，
	// 模型真正需要某个工作流时，再通过 load_skill 工具读取完整 SKILL.md。
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
	fmt.Fprintf(os.Stderr, "[s05] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	skillsRoot := filepath.Join(workdir, "skills")
	loader, err := skills.NewLoader(skillsRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill loader error: %v\n", err)
		os.Exit(1)
	}

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	skills.RegisterLoadSkill(reg, loader)
	skills.RegisterCreateSkill(reg, loader)
	skills.RegisterUpdateSkill(reg, loader)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    systemPrompt(workdir, loader.Descriptions()),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 50,
		Trace:     os.Stderr,
	}
	fmt.Fprintf(os.Stderr, "[s05] workdir=%s skills_root=%s prompt=%q\n", workdir, skillsRoot, prompt)
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
		return "", fmt.Errorf("usage: s05-skill-loading <prompt>")
	}
	return prompt, nil
}

func systemPrompt(workdir, skillDescriptions string) string {
	// 这里是 s05 的关键：只给模型一个低成本 skill 目录。
	// 如果用户任务需要某个 skill，模型必须显式调用 load_skill，把完整内容作为 tool_result 拉进上下文。
	return fmt.Sprintf(`You are a coding agent running in %s.

Skills available:
%s

Use load_skill before applying a skill's detailed workflow.
When the user asks what skills are available, list the names and descriptions from Skills available.
When the user asks you to summarize or follow a local skill, call load_skill first and then use the returned instructions.
When you summarize a loaded skill, include the <path> value from the load_skill result.
When the user asks you to preserve a reusable workflow or lesson, call create_skill with a concise name, a trigger-focused description, and reusable markdown content.
When the user asks you to improve or update an existing local skill, call update_skill with the existing skill name and the complete improved markdown content.
After creating a skill, call load_skill with the created skill name to verify it can be reused.
After updating a skill, call load_skill with the updated skill name to verify the improved content.
Prefer read_file, write_file, and edit_file for file operations.
Use bash only when a shell command is genuinely needed.
When finished, answer the user directly.`, workdir, skillDescriptions)
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
