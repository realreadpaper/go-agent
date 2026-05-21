package team

import (
	"context"
	"strings"
	"testing"
	"time"

	"learn-claude-code-go/internal/tools"
)

type fakeRunner struct {
	done chan struct{}
}

func (r *fakeRunner) Run(ctx context.Context, teammate Teammate, prompt string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-r.done:
		return "finished " + teammate.Name + ": " + prompt, nil
	}
}

func TestManagerSpawnsTeammatePersistsConfigAndGoesIdle(t *testing.T) {
	runner := &fakeRunner{done: make(chan struct{})}
	manager, err := NewManager(t.TempDir(), runner)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	member, err := manager.Spawn("alice", "coder", "implement feature")
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}
	if member.Name != "alice" || member.Role != "coder" || member.Status != StatusWorking {
		t.Fatalf("spawned member = %+v", member)
	}

	config, err := manager.Config()
	if err != nil {
		t.Fatalf("Config returned error: %v", err)
	}
	if len(config.Members) != 1 || config.Members[0].Status != StatusWorking {
		t.Fatalf("config after spawn = %+v", config)
	}

	close(runner.done)
	waitForMemberStatus(t, manager, "alice", StatusIdle)
}

func TestManagerMessageToolsAndInboxDrain(t *testing.T) {
	manager, err := NewManager(t.TempDir(), &fakeRunner{done: make(chan struct{})})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	if _, err := manager.Spawn("alice", "coder", "stand by"); err != nil {
		t.Fatalf("Spawn alice returned error: %v", err)
	}
	if _, err := manager.Spawn("bob", "reviewer", "stand by"); err != nil {
		t.Fatalf("Spawn bob returned error: %v", err)
	}

	if err := manager.Send("lead", "bob", "status?"); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if err := manager.Broadcast("lead", "hello team"); err != nil {
		t.Fatalf("Broadcast returned error: %v", err)
	}
	bobMessages, err := manager.ReadInbox("bob")
	if err != nil {
		t.Fatalf("ReadInbox bob returned error: %v", err)
	}
	if len(bobMessages) != 2 {
		t.Fatalf("bob messages = %+v, want direct and broadcast", bobMessages)
	}
	aliceMessages, err := manager.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox alice returned error: %v", err)
	}
	if len(aliceMessages) != 1 || aliceMessages[0].Content != "hello team" {
		t.Fatalf("alice messages = %+v, want broadcast", aliceMessages)
	}
}

func TestRegisterTeamTools(t *testing.T) {
	manager, err := NewManager(t.TempDir(), &fakeRunner{done: make(chan struct{})})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	reg := tools.NewRegistry()
	Register(reg, manager)

	out := reg.Run("spawn_teammate", map[string]any{
		"name":   "bob",
		"role":   "reviewer",
		"prompt": "wait for messages",
	})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"name": "bob"`) {
		t.Fatalf("spawn_teammate output = %q", out)
	}
	out = reg.Run("send_message", map[string]any{"to": "bob", "content": "status?"})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, "sent") {
		t.Fatalf("send_message output = %q", out)
	}
	out = reg.Run("read_inbox", map[string]any{"name": "bob"})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, "status?") {
		t.Fatalf("read_inbox output = %q", out)
	}
	out = reg.Run("list_teammates", map[string]any{})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"members"`) {
		t.Fatalf("list_teammates output = %q", out)
	}
}

func TestRegisterTeamToolsUsesExplicitSender(t *testing.T) {
	manager, err := NewManager(t.TempDir(), &fakeRunner{done: make(chan struct{})})
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	reg := tools.NewRegistry()
	RegisterForSender(reg, manager, "alice")

	out := reg.Run("send_message", map[string]any{"to": "bob", "content": "ready for review"})
	if strings.HasPrefix(out, "Error:") {
		t.Fatalf("send_message output = %q", out)
	}
	messages, err := manager.ReadInbox("bob")
	if err != nil {
		t.Fatalf("ReadInbox returned error: %v", err)
	}
	if len(messages) != 1 || messages[0].From != "alice" {
		t.Fatalf("messages = %+v, want one message from alice", messages)
	}
}

func waitForMemberStatus(t *testing.T, manager *Manager, name, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		member, err := manager.Get(name)
		if err != nil {
			t.Fatalf("Get returned error: %v", err)
		}
		if member.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	member, _ := manager.Get(name)
	t.Fatalf("member %s status = %s, want %s", name, member.Status, want)
}
