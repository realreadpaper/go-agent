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
	"learn-claude-code-go/internal/background"
	"learn-claude-code-go/internal/compact"
	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/protocols"
	"learn-claude-code-go/internal/skills"
	"learn-claude-code-go/internal/subagent"
	"learn-claude-code-go/internal/tasks"
	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/todo"
	"learn-claude-code-go/internal/tools"
	"learn-claude-code-go/internal/worktree"
)

func main() {
	// sfull 是最终组装层：它只负责创建 managers、注册工具、挂 hook 和启动单次 prompt/REPL。
	// 具体能力仍在 internal/* 包中，避免最终命令变成一份不可测试的大杂烩。
	loadNearestDotEnv()

	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workdir error: %v\n", err)
		os.Exit(1)
	}
	args := os.Args[1:]
	if len(args) > 0 {
		line := strings.TrimSpace(strings.Join(args, " "))
		if isReadOnlyCommand(line) {
			if err := runReadOnlyCommand(workdir, line, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "sfull error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}
	if len(args) == 0 && !isTerminal(os.Stdin) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stdin error: %v\n", err)
			os.Exit(1)
		}
		prompt := strings.TrimSpace(string(data))
		if prompt == "" {
			fmt.Fprintln(os.Stderr, "usage: sfull <prompt|/tasks|/team|/inbox|/compact|/worktrees|/exit>")
			os.Exit(2)
		}
		if isReadOnlyCommand(prompt) {
			if err := runReadOnlyCommand(workdir, prompt, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "sfull error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		args = []string{prompt}
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
	fmt.Fprintf(os.Stderr, "[sfull] provider=%s api_style=%s model=%s base_url=%s trace_raw_api=%t\n", cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, cfg.TraceRawAPI)

	app, err := newApp(workdir, cfg, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "app init error: %v\n", err)
		os.Exit(1)
	}

	if len(args) > 0 {
		if err := app.runLine(strings.Join(args, " "), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "sfull error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := app.repl(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "sfull error: %v\n", err)
		os.Exit(1)
	}
}

func isReadOnlyCommand(line string) bool {
	switch strings.TrimSpace(line) {
	case "/tasks", "/team", "/inbox", "/worktrees", "/exit":
		return true
	default:
		return false
	}
}

func runReadOnlyCommand(workdir, line string, w io.Writer) error {
	teamManager, err := team.NewManager(workdir, nil)
	if err != nil {
		return err
	}
	taskManager, err := tasks.LoadManager(workdir)
	if err != nil {
		return err
	}
	worktreeManager, err := worktree.NewManager(workdir, taskManager)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(line) {
	case "/tasks":
		ready, err := taskManager.ListReady()
		if err != nil {
			return err
		}
		return writeJSON(w, map[string]any{"tasks": taskManager.List(), "ready": ready})
	case "/team":
		config, err := teamManager.Config()
		if err != nil {
			return err
		}
		return writeJSON(w, config)
	case "/inbox":
		messages, err := teamManager.ReadInbox("lead")
		if err != nil {
			return err
		}
		return writeJSON(w, messages)
	case "/worktrees":
		return writeJSON(w, map[string]any{"worktrees": worktreeManager.List()})
	case "/exit":
		return nil
	default:
		return fmt.Errorf("unknown read-only command: %s", line)
	}
}

type app struct {
	workdir    string
	cfg        llm.Config
	client     llm.Client
	reg        *tools.Registry
	loop       *agent.Loop
	messages   []llm.Message
	todo       *todo.Manager
	tasks      *tasks.Manager
	background *background.Manager
	team       *team.Manager
	protocols  *protocols.Tracker
	compact    *compact.Manager
	worktree   *worktree.Manager
}

func newApp(workdir string, cfg llm.Config, client llm.Client) (*app, error) {
	taskManager, err := tasks.LoadManager(workdir)
	if err != nil {
		return nil, err
	}
	tracker, err := protocols.NewTracker(workdir)
	if err != nil {
		return nil, err
	}
	todoManager := todo.NewPersistentManager(workdir)
	backgroundManager := background.NewManager(workdir)
	worktreeManager, err := worktree.NewManager(workdir, taskManager)
	if err != nil {
		return nil, err
	}
	compactManager := &compact.Manager{
		Client:        client,
		Model:         cfg.Model,
		System:        "You summarize coding agent state for context compaction.",
		MaxTokens:     cfg.MaxTokens,
		TokenLimit:    24_000,
		TranscriptDir: filepath.Join(workdir, ".transcripts"),
	}

	runner := &teammateRunner{
		client:      client,
		model:       cfg.Model,
		maxTokens:   cfg.MaxTokens,
		workdir:     workdir,
		taskManager: taskManager,
		tracker:     tracker,
		trace:       os.Stderr,
	}
	teamManager, err := team.NewManager(workdir, runner)
	if err != nil {
		return nil, err
	}
	runner.manager = teamManager

	reg := tools.NewRegistry()
	registerFullTools(reg, workdir, cfg, client, todoManager, taskManager, backgroundManager, teamManager, tracker, compactManager, worktreeManager)

	nag := agent.WithTodoNag(3)
	loop := &agent.Loop{
		Client:    client,
		Model:     cfg.Model,
		System:    systemPrompt(workdir, availableSkills(workdir)),
		Tools:     reg,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 100,
		BeforeCall: []agent.BeforeCallHook{
			backgroundManager.BeforeCall,
			compactManager.MicroCompact,
			compactManager.AutoCompactIfNeeded,
			nag.BeforeCall,
		},
		AfterTool: []agent.AfterToolHook{nag.AfterTool},
		Trace:     prefixedWriter{prefix: "[sfull] ", w: os.Stderr},
	}
	return &app{
		workdir:    workdir,
		cfg:        cfg,
		client:     client,
		reg:        reg,
		loop:       loop,
		messages:   nil,
		todo:       todoManager,
		tasks:      taskManager,
		background: backgroundManager,
		team:       teamManager,
		protocols:  tracker,
		compact:    compactManager,
		worktree:   worktreeManager,
	}, nil
}

func registerFullTools(reg *tools.Registry, workdir string, cfg llm.Config, client llm.Client, todoManager *todo.Manager, taskManager *tasks.Manager, backgroundManager *background.Manager, teamManager *team.Manager, tracker *protocols.Tracker, compactManager *compact.Manager, worktreeManager *worktree.Manager) {
	tools.RegisterBash(reg, workdir)
	tools.RegisterFileTools(reg, workdir)
	todo.Register(reg, todoManager)
	tasks.Register(reg, taskManager)
	background.Register(reg, backgroundManager)
	team.Register(reg, teamManager)
	protocols.Register(reg, tracker, teamManager, "lead")
	compact.RegisterCompact(reg, compactManager)
	worktree.Register(reg, worktreeManager)
	if loader, err := skills.NewLoader(filepath.Join(workdir, "skills")); err == nil {
		skills.RegisterLoadSkill(reg, loader)
	}

	childTools := tools.NewRegistry()
	tools.RegisterBash(childTools, workdir)
	tools.RegisterFileTools(childTools, workdir)
	tasks.Register(childTools, taskManager)
	childRunner := &subagent.Runner{
		Client:    client,
		Model:     cfg.Model,
		System:    "You are a focused subagent. Complete the delegated task and return a concise summary.",
		Tools:     childTools,
		MaxTokens: cfg.MaxTokens,
		MaxRounds: 30,
		Trace:     prefixedWriter{prefix: "[subagent] ", w: os.Stderr},
	}
	subagent.RegisterTask(reg, childRunner)
}

func (a *app) runLine(line string, w io.Writer) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if handled, err := a.handleCommand(line, w); handled {
		return err
	}
	a.messages = append(a.messages, llm.Message{Role: "user", Content: line})
	var resp llm.Response
	var err error
	a.messages, resp, err = a.loop.Run(a.messages)
	if err != nil {
		return err
	}
	printResponse(w, resp)
	return nil
}

func (a *app) repl(r io.Reader, w io.Writer) error {
	fmt.Fprintln(w, "sfull REPL. Type /tasks, /team, /inbox, /compact, /worktrees, or /exit.")
	scanner := bufio.NewScanner(r)
	for {
		fmt.Fprint(w, "> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" {
			fmt.Fprintln(w, "bye")
			return nil
		}
		if err := a.runLine(line, w); err != nil {
			fmt.Fprintf(w, "Error: %v\n", err)
		}
	}
}

func (a *app) handleCommand(line string, w io.Writer) (bool, error) {
	switch line {
	case "/compact":
		a.compact.RequestManual()
		return true, writeJSON(w, map[string]any{"compact": "requested"})
	case "/tasks":
		ready, err := a.tasks.ListReady()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, map[string]any{"tasks": a.tasks.List(), "ready": ready})
	case "/team":
		config, err := a.team.Config()
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, config)
	case "/inbox":
		messages, err := a.team.ReadInbox("lead")
		if err != nil {
			return true, err
		}
		return true, writeJSON(w, messages)
	case "/worktrees":
		return true, writeJSON(w, map[string]any{"worktrees": a.worktree.List()})
	case "/exit":
		return true, nil
	default:
		return false, nil
	}
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

func systemPrompt(workdir, skillList string) string {
	return fmt.Sprintf(`You are a full Go agent harness running in %s.
You have local tools for shell, files, todo planning, task graph, subagents, skill loading, context compaction, background tasks, teams, request protocols, and autonomous task claiming.
Use todo for multi-step work, task_* for durable work, background_* for slow commands, task for focused subagents, and team/protocol tools for teammate coordination.
Available skills:
%s
Use worktree_* tools when a task needs an isolated git worktree.
When finished, answer the user directly and summarize files, tasks, teammates, and protocol request ids when relevant.`, workdir, skillList)
}

func teammateSystemPrompt(workdir string, teammate team.Teammate) string {
	return fmt.Sprintf(`You are teammate %s, role: %s, working in %s.
When you finish current work or have no direct assignment, call idle.
Unread inbox messages may be injected inside <team-inbox>.
If idle returns an auto-claimed task, work on it and report status to lead.
Use protocol tools for shutdown and plan approval when needed.`, teammate.Name, teammate.Role, workdir)
}

func availableSkills(workdir string) string {
	loader, err := skills.NewLoader(filepath.Join(workdir, "skills"))
	if err != nil {
		return "(no skills directory found)"
	}
	return loader.Descriptions()
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

func isTerminal(file *os.File) bool {
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
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
