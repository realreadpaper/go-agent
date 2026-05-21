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
	"learn-claude-code-go/internal/protocols"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s10 在 s09 团队邮箱之上增加 request-response 协议。
	// 普通消息继续走 inbox；需要审批、关机这类可追踪动作时，必须带 request_id 并进入 FSM。
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
	localTeam, localTracker, err := loadLocalState(workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state error: %v\n", err)
		os.Exit(1)
	}
	if handled, err := handleCommand(prompt, localTeam, localTracker, os.Stdout); handled {
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
	fmt.Fprintf(os.Stderr, "[s10] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	runner := &teammateRunner{
		client:    client,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		workdir:   workdir,
		tracker:   localTracker,
		trace:     os.Stderr,
	}
	teamManager, err := team.NewManager(workdir, runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "team manager error: %v\n", err)
		os.Exit(1)
	}
	runner.manager = teamManager

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	team.Register(reg, teamManager)
	protocols.Register(reg, localTracker, teamManager, "lead")

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    leadSystemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 60,
		Trace:     prefixedWriter{prefix: "[lead] ", w: os.Stderr},
	}
	fmt.Fprintf(os.Stderr, "[s10] workdir=%s team_dir=%s prompt=%q\n", workdir, filepath.Join(workdir, ".team"), prompt)
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

type teammateRunner struct {
	client    llm.Client
	model     string
	maxTokens int
	workdir   string
	manager   *team.Manager
	tracker   *protocols.Tracker
	trace     io.Writer
}

func (r *teammateRunner) Run(ctx context.Context, teammate team.Teammate, prompt string) (string, error) {
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, r.workdir)
	tools.RegisterFileTools(reg, r.workdir)
	team.RegisterForSender(reg, r.manager, teammate.Name)
	protocols.Register(reg, r.tracker, r.manager, teammate.Name)

	loop := &agent.Loop{
		Client:     r.client,
		Model:      r.model,
		System:     teammateSystemPrompt(r.workdir, teammate),
		Tools:      reg,
		MaxTokens:  r.maxTokens,
		MaxRounds:  40,
		BeforeCall: []agent.BeforeCallHook{r.inboxHook(teammate.Name)},
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

func (r *teammateRunner) inboxHook(name string) agent.BeforeCallHook {
	return func(messages *[]llm.Message) error {
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
}

func loadLocalState(workdir string) (*team.Manager, *protocols.Tracker, error) {
	teamManager, err := team.NewManager(workdir, nil)
	if err != nil {
		return nil, nil, err
	}
	tracker, err := protocols.NewTracker(workdir)
	if err != nil {
		return nil, nil, err
	}
	return teamManager, tracker, nil
}

func handleCommand(prompt string, teamManager *team.Manager, tracker *protocols.Tracker, w io.Writer) (bool, error) {
	switch strings.TrimSpace(prompt) {
	case "/team":
		config, err := teamManager.Config()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, config)
	case "/inbox":
		messages, err := teamManager.ReadInbox("lead")
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, messages)
	case "/requests":
		return true, writeJSON(w, map[string]any{"requests": tracker.List()})
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
		return "", fmt.Errorf("usage: s10-team-protocols <prompt|/team|/inbox|/requests>")
	}
	return prompt, nil
}

func leadSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are the lead coding agent running in %s.
Use team tools to create teammates and exchange ordinary messages.
Use shutdown_request, shutdown_response, plan_submit, and plan_review for tracked request-response protocols.
Use request_status to inspect pending, approved, and rejected protocol requests.
Shutdown and plan review actions must mention request IDs in the final answer.
Prefer file tools for file work and bash only for commands that need a shell.`, workdir)
}

func teammateSystemPrompt(workdir string, teammate team.Teammate) string {
	return fmt.Sprintf(`You are teammate %s, role: %s, working in %s.
Unread inbox messages are injected inside <team-inbox>.
Protocol messages are JSON objects with type=protocol_request or type=protocol_response and a request object.
For shutdown requests, finish any current thought, call shutdown_response with approve=true or approve=false, and explain your reason.
For risky plans, call plan_submit before making the change and wait for review feedback.`, teammate.Name, teammate.Role, workdir)
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
