package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s09 把单次 subagent 升级成有名字、有角色、有文件 inbox 的团队成员。
	// 主 agent 只通过 team tools 管理队友；队友之间通过 .team/inbox/*.jsonl 显式通信。
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
	if isLocalCommand(prompt) {
		localManager, err := team.NewManager(workdir, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "team manager error: %v\n", err)
			os.Exit(1)
		}
		if handled, err := handleCommand(prompt, localManager, os.Stdout); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
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
	fmt.Fprintf(os.Stderr, "[s09] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	runner := &teammateRunner{
		client:    client,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		workdir:   workdir,
		trace:     os.Stderr,
	}
	teamManager, err := team.NewManager(workdir, runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "team manager error: %v\n", err)
		os.Exit(1)
	}
	runner.manager = teamManager

	if handled, err := handleCommand(prompt, teamManager, os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	team.Register(reg, teamManager)

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    leadSystemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 50,
		Trace:     prefixedWriter{prefix: "[lead] ", w: os.Stderr},
	}
	fmt.Fprintf(os.Stderr, "[s09] workdir=%s team_dir=%s prompt=%q\n", workdir, filepath.Join(workdir, ".team"), prompt)
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

// teammateRunner 是 Manager 依赖的真实执行器。
// 每次 spawn 都会创建一个独立 agent loop；该 loop 有自己的 messages、工具集合和 trace 前缀。
type teammateRunner struct {
	client    llm.Client
	model     string
	maxTokens int
	workdir   string
	manager   *team.Manager
	trace     io.Writer
}

func (r *teammateRunner) Run(ctx context.Context, teammate team.Teammate, prompt string) (string, error) {
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, r.workdir)
	tools.RegisterFileTools(reg, r.workdir)
	team.RegisterForSender(reg, r.manager, teammate.Name)

	inboxHook := func(messages *[]llm.Message) error {
		return r.injectInbox(teammate.Name, messages)
	}
	loop := &agent.Loop{
		Client:     r.client,
		Model:      r.model,
		System:     teammateSystemPrompt(r.workdir, teammate),
		Tools:      reg,
		MaxTokens:  r.maxTokens,
		MaxRounds:  30,
		BeforeCall: []agent.BeforeCallHook{inboxHook},
		Trace:      prefixedWriter{prefix: fmt.Sprintf("[%s] ", teammate.Name), w: r.trace},
	}
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	return responseText(resp), nil
}

func (r *teammateRunner) injectInbox(name string, messages *[]llm.Message) error {
	inbox, err := r.manager.ReadInbox(name)
	if err != nil {
		return err
	}
	if len(inbox) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("<team-inbox>\n")
	for _, message := range inbox {
		b.WriteString(fmt.Sprintf("from=%s at=%s\n%s\n\n", message.From, message.CreatedAt, message.Content))
	}
	b.WriteString("</team-inbox>")
	*messages = append(*messages, llm.Message{Role: "user", Content: b.String()})
	return nil
}

func isLocalCommand(prompt string) bool {
	switch strings.TrimSpace(prompt) {
	case "/team", "/inbox":
		return true
	default:
		return false
	}
}

func handleCommand(prompt string, manager *team.Manager, w io.Writer) (bool, error) {
	switch strings.TrimSpace(prompt) {
	case "/team":
		config, err := manager.Config()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, config)
	case "/inbox":
		messages, err := manager.ReadInbox("lead")
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, messages)
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
		return "", fmt.Errorf("usage: s09-agent-teams <prompt|/team|/inbox>")
	}
	return prompt, nil
}

func leadSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are the lead coding agent running in %s.
Use spawn_teammate to create named teammate agents when work benefits from parallel roles such as coder, reviewer, researcher, or tester.
Use list_teammates to inspect the roster stored in .team/config.json.
Use send_message and broadcast for explicit async communication through .team/inbox/*.jsonl.
Use read_inbox with name="lead" to drain replies sent back to you.
Prefer read_file, write_file, and edit_file for file operations.
Use bash only when a shell command is genuinely needed.
When finished, answer the user directly and mention teammate names, inbox observations, and any files changed.`, workdir)
}

func teammateSystemPrompt(workdir string, teammate team.Teammate) string {
	return fmt.Sprintf(`You are teammate %s, role: %s, working in %s.
You have your own private conversation history. Other agents cannot see it unless you send a message.
Before each model call, unread messages from your .team inbox may be injected inside <team-inbox>.
Use send_message to report concise status or results to lead, or to coordinate with another teammate.
Use list_teammates when you need to know who exists.
Use read_file, write_file, edit_file, and bash only for work directly related to your assigned prompt.
Return a concise final summary when your current assignment is complete.`, teammate.Name, teammate.Role, workdir)
}

func printResponse(w io.Writer, resp llm.Response) {
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			fmt.Fprintln(w, block.Text)
		}
	}
}

func responseText(resp llm.Response) string {
	var parts []string
	for _, block := range resp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n\n")
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
