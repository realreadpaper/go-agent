package compact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"learn-claude-code-go/internal/llm"
	"learn-claude-code-go/internal/tools"
)

const defaultKeepRecent = 3

// Manager 管理 s06 的三层上下文压缩。
// 它不改变 agent.Loop 的主流程，而是作为 BeforeCall hook 修改 messages：
// micro compact 保留最近工具结果、auto/manual compact 把完整历史落盘后替换成摘要。
type Manager struct {
	Client        llm.Client
	Model         string
	System        string
	MaxTokens     int
	TokenLimit    int
	KeepRecent    int
	TranscriptDir string

	mu              sync.Mutex
	manualRequested bool
}

// EstimateTokens 用 JSON 字节长度 / 4 做教学版 token 估算。
// 真实产品会使用模型对应 tokenizer；这里保留近似实现，让读者先理解触发机制。
func EstimateTokens(messages []llm.Message) int {
	data, err := json.Marshal(messages)
	if err != nil {
		return 0
	}
	if len(data) == 0 {
		return 0
	}
	return len(data)/4 + 1
}

// MicroCompact 把较旧的 tool_result 内容替换成短占位符。
// 这样模型仍知道“之前用过哪个工具”，但不会每轮反复携带大段旧输出。
func (m *Manager) MicroCompact(messages *[]llm.Message) error {
	if messages == nil {
		return nil
	}
	keep := m.KeepRecent
	if keep <= 0 {
		keep = defaultKeepRecent
	}
	idToTool := toolUseNames(*messages)
	positions := toolResultPositions(*messages)
	cutoff := len(positions) - keep
	if cutoff <= 0 {
		return nil
	}
	for _, pos := range positions[:cutoff] {
		results := (*messages)[pos.messageIndex].Content.([]llm.ToolResult)
		result := results[pos.resultIndex]
		toolName := idToTool[result.ToolUseID]
		if toolName == "" {
			toolName = "unknown"
		}
		results[pos.resultIndex].Content = fmt.Sprintf("[Previous: used %s]", toolName)
		(*messages)[pos.messageIndex].Content = results
	}
	return nil
}

// AutoCompactIfNeeded 在超过 token 阈值或 manual compact 被请求时压缩上下文。
// 压缩前完整 transcript 会写入磁盘，活跃 messages 只保留一条摘要消息。
func (m *Manager) AutoCompactIfNeeded(messages *[]llm.Message) error {
	if messages == nil {
		return nil
	}
	manual := m.consumeManualRequest()
	limit := m.TokenLimit
	if limit <= 0 {
		limit = 24_000
	}
	if !manual && EstimateTokens(*messages) < limit {
		return nil
	}
	return m.Compact(messages)
}

// Compact 强制执行摘要压缩。
// 这个方法会调用 LLM 生成摘要，因此测试中通常传入脚本化 client。
func (m *Manager) Compact(messages *[]llm.Message) error {
	if err := m.writeTranscript(*messages); err != nil {
		return err
	}
	summary, err := m.summarize(*messages)
	if err != nil {
		return err
	}
	*messages = []llm.Message{{
		Role:    "user",
		Content: "[Compressed]\n\n" + summary,
	}}
	return nil
}

// RequestManual 由 compact 工具调用。
// 工具 handler 本身不能直接拿到 loop 的 messages，所以只设置标记，下一轮 BeforeCall 再真正压缩。
func (m *Manager) RequestManual() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.manualRequested = true
}

// RegisterCompact 注册模型可主动调用的 compact 工具。
func RegisterCompact(reg *tools.Registry, manager *Manager) {
	reg.Register(tools.Tool{
		Spec: tools.Spec("compact", "Request conversation context compaction before the next model call.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		Handler: func(input map[string]any) (string, error) {
			manager.RequestManual()
			return "Manual compact requested. The harness will summarize the conversation before the next model call.", nil
		},
	})
}

func (m *Manager) consumeManualRequest() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	manual := m.manualRequested
	m.manualRequested = false
	return manual
}

func (m *Manager) summarize(messages []llm.Message) (string, error) {
	if m.Client == nil {
		return "", fmt.Errorf("compact client is required")
	}
	reqMessages := append([]llm.Message(nil), messages...)
	reqMessages = append(reqMessages, llm.Message{
		Role:    "user",
		Content: "Summarize the current task state, important files, decisions, and next steps. Keep enough detail so the agent can continue after context compaction.",
	})
	resp, err := m.Client.Create(llm.Request{
		Model:     m.Model,
		System:    m.System,
		Messages:  reqMessages,
		MaxTokens: m.MaxTokens,
	})
	if err != nil {
		return "", err
	}
	var parts []string
	for _, block := range resp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	if len(parts) == 0 {
		return "(empty compact summary)", nil
	}
	return strings.Join(parts, "\n\n"), nil
}

func (m *Manager) writeTranscript(messages []llm.Message) error {
	dir := m.TranscriptDir
	if dir == "" {
		dir = ".transcripts"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("transcript_%d.jsonl", time.Now().UnixNano())
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}
	return nil
}

type resultPosition struct {
	messageIndex int
	resultIndex  int
}

func toolResultPositions(messages []llm.Message) []resultPosition {
	var positions []resultPosition
	for i, msg := range messages {
		results, ok := msg.Content.([]llm.ToolResult)
		if !ok {
			continue
		}
		for j := range results {
			positions = append(positions, resultPosition{messageIndex: i, resultIndex: j})
		}
	}
	return positions
}

func toolUseNames(messages []llm.Message) map[string]string {
	names := map[string]string{}
	for _, msg := range messages {
		blocks, ok := msg.Content.([]llm.ContentBlock)
		if !ok {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_use" && block.ID != "" {
				names[block.ID] = block.Name
			}
		}
	}
	return names
}
