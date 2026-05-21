package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageBusSendAppendsJSONLAndReadDrainsInbox(t *testing.T) {
	bus := NewMessageBus(t.TempDir())

	if err := bus.Send(Message{From: "lead", To: "bob", Content: "status?"}); err != nil {
		t.Fatalf("Send first returned error: %v", err)
	}
	if err := bus.Send(Message{From: "alice", To: "bob", Content: "please review"}); err != nil {
		t.Fatalf("Send second returned error: %v", err)
	}

	path := filepath.Join(bus.InboxDir(), "bob.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", path, err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 2 {
		t.Fatalf("inbox line count = %d, want 2\n%s", lines, data)
	}

	messages, err := bus.ReadInbox("bob")
	if err != nil {
		t.Fatalf("ReadInbox returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %+v, want 2 messages", messages)
	}
	if messages[0].From != "lead" || messages[0].Content != "status?" {
		t.Fatalf("first message = %+v", messages[0])
	}
	if stat, err := os.Stat(path); err != nil {
		t.Fatalf("Stat drained inbox returned error: %v", err)
	} else if stat.Size() != 0 {
		t.Fatalf("drained inbox size = %d, want 0", stat.Size())
	}
}

func TestMessageBusEmptyInboxReturnsEmptySlice(t *testing.T) {
	bus := NewMessageBus(t.TempDir())

	messages, err := bus.ReadInbox("missing")
	if err != nil {
		t.Fatalf("ReadInbox returned error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %+v, want empty", messages)
	}
}
