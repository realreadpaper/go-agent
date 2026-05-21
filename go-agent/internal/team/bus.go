package team

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Message 是团队成员之间通过 JSONL inbox 传递的显式消息。
// 团队 agent 不共享上下文，只共享这种小而明确的通信事件。
type Message struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// MessageBus 负责 .team/inbox/*.jsonl 的 append 和 drain。
// 用文件作为消息总线，是为了让队友通信能跨进程、跨上下文压缩被观察和恢复。
type MessageBus struct {
	mu       sync.Mutex
	root     string
	inboxDir string
}

func NewMessageBus(root string) *MessageBus {
	return &MessageBus{
		root:     root,
		inboxDir: filepath.Join(root, ".team", "inbox"),
	}
}

func (b *MessageBus) InboxDir() string {
	return b.inboxDir
}

func (b *MessageBus) Send(message Message) error {
	message.From = strings.TrimSpace(message.From)
	message.To = normalizeName(message.To)
	message.Content = strings.TrimSpace(message.Content)
	if message.From == "" {
		message.From = "lead"
	}
	if message.To == "" {
		return fmt.Errorf("to is required")
	}
	if message.Content == "" {
		return fmt.Errorf("content is required")
	}
	if message.CreatedAt == "" {
		message.CreatedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.MkdirAll(b.inboxDir, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(b.inboxPath(message.To), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (b *MessageBus) ReadInbox(name string) ([]Message, error) {
	name = normalizeName(name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.MkdirAll(b.inboxDir, 0o755); err != nil {
		return nil, err
	}
	path := b.inboxPath(name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var messages []Message
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message Message
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := file.Truncate(0); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	return messages, nil
}

func (b *MessageBus) inboxPath(name string) string {
	return filepath.Join(b.inboxDir, normalizeName(name)+".jsonl")
}

func normalizeName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}
