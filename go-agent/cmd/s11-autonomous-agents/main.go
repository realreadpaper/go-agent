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
	"time"

	"learn-claude-code-go/internal/agent"
	"learn-claude-code-go/internal/autonomous"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/protocols"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

func main() {
	// s11 让队友做完当前工作后进入受控 IDLE phase。
	// IDLE 不让模型无限空转：先读 inbox，再认领 ready task，超时仍然没有工作就 shutdown。
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
	teamManager, taskManager, tracker, err := loadState(workdir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state error: %v\n", err)
		os.Exit(1)
	}
	if handled, err := handleCommand(prompt, teamManager, taskManager, tracker, os.Stdout); handled {
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
	fmt.Fprintf(os.Stderr, "[s11] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	runner := &teammateRunner{
		client:      client,
		model:       cfg.Model,
		maxTokens:   cfg.MaxTokens,
		workdir:     workdir,
		taskManager: taskManager,
		tracker:     tracker,
		trace:       os.Stderr,
	}
	teamManager, taskManager, tracker, err = loadState(workdir, runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state error: %v\n", err)
		os.Exit(1)
	}
	runner.manager = teamManager
	runner.taskManager = taskManager
	runner.tracker = tracker

	reg := tools.NewRegistry()
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	team.Register(reg, teamManager)
	tasks.Register(reg, taskManager)
	protocols.Register(reg, tracker, teamManager, "lead")

	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    leadSystemPrompt(workdir),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 80,
		Trace:     prefixedWriter{prefix: "[lead] ", w: os.Stderr},
	}
	fmt.Fprintf(os.Stderr, "[s11] workdir=%s task_dir=%s team_dir=%s prompt=%q\n", workdir, taskManager.Dir(), filepath.Join(workdir, ".team"), prompt)
	_, resp, err := loop.Run([]llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	printResponse(os.Stdout, resp)
}

type teammateRunner struct {
	client      llm.Client
	model       string
	maxTokens   int
	workdir     string
	manager     *team.Manager
	taskManager *tasks.Manager
	tracker     *protocols.Tracker
	trace       io.Writer
}

func (r *teammateRunner) Run(ctx context.Context, teammate team.Teammate, prompt string) (string, error) {
	reg := tools.NewRegistry()
	tools.RegisterBash(reg, r.workdir)
	tools.RegisterFileTools(reg, r.workdir)
	team.RegisterForSender(reg, r.manager, teammate.Name)
	tasks.Register(reg, r.taskManager)
	protocols.Register(reg, r.tracker, r.manager, teammate.Name)
	controller := autonomous.NewController(r.manager, r.taskManager, autonomous.IdleConfig{
		PollInterval: time.Second,
		Timeout:      15 * time.Second,
	})
	autonomous.Register(reg, controller, teammate)

	loop := &agent.Loop{
		Client:    r.client,
		Model:     r.model,
		System:    teammateSystemPrompt(r.workdir, teammate),
		Tools:     reg,
		MaxTokens: r.maxTokens,
		MaxRounds: 60,
		BeforeCall: []agent.BeforeCallHook{
			autonomous.IdentityHook(teammate, "default"),
			r.inboxHook(teammate.Name),
		},
		Trace: prefixedWriter{prefix: fmt.Sprintf("[%s] ", teammate.Name), w: r.trace},
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

func loadState(workdir string, runner team.Runner) (*team.Manager, *tasks.Manager, *protocols.Tracker, error) {
	teamManager, err := team.NewManager(workdir, runner)
	if err != nil {
		return nil, nil, nil, err
	}
	taskManager, err := tasks.LoadManager(workdir)
	if err != nil {
		return nil, nil, nil, err
	}
	tracker, err := protocols.NewTracker(workdir)
	if err != nil {
		return nil, nil, nil, err
	}
	return teamManager, taskManager, tracker, nil
}

func handleCommand(prompt string, teamManager *team.Manager, taskManager *tasks.Manager, tracker *protocols.Tracker, w io.Writer) (bool, error) {
	switch strings.TrimSpace(prompt) {
	case "/team":
		config, err := teamManager.Config()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, config)
	case "/tasks":
		ready, err := taskManager.ListReady()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, map[string]any{"tasks": taskManager.List(), "ready": ready})
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
		return "", fmt.Errorf("usage: s11-autonomous-agents <prompt|/team|/tasks|/inbox|/requests>")
	}
	return prompt, nil
}

func leadSystemPrompt(workdir string) string {
	return fmt.Sprintf(`You are the lead coding agent running in %s.
Use task_create to create durable work on the task board.
Use spawn_teammate to create persistent teammates. Give each teammate an initial prompt that tells them to call idle when they have no immediate work.
Use task_list to inspect ready tasks, and list_teammates to inspect the team.
Autonomous teammates can call idle to wait for inbox messages or auto-claim ready tasks.
When finished, summarize which teammates exist and which tasks were claimed.`, workdir)
}

func teammateSystemPrompt(workdir string, teammate team.Teammate) string {
	return fmt.Sprintf(`You are teammate %s, role: %s, working in %s.
When you finish current work or have no direct assignment, call idle.
The idle tool returns either inbox work, an auto-claimed task, or an idle timeout.
If idle returns an auto-claimed task, work on that task and report status to lead.
Use claim_task only for a specific ready task. Do not claim blocked or owned tasks.
Unread inbox messages may be injected inside <team-inbox>; identity may be re-injected after compaction.`, teammate.Name, teammate.Role, workdir)
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
