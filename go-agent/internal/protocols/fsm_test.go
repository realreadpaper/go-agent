package protocols

import (
	"strings"
	"testing"

	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

func TestTrackerCreatesPendingRequestAndTransitions(t *testing.T) {
	tracker, err := NewTracker(t.TempDir())
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}

	req, err := tracker.Create("shutdown", "lead", "alice", map[string]any{"reason": "done"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if req.ID != "req-000001" || req.Status != StatusPending {
		t.Fatalf("created request = %+v, want req-000001 pending", req)
	}

	approved, err := tracker.Approve(req.ID, "ok")
	if err != nil {
		t.Fatalf("Approve returned error: %v", err)
	}
	if approved.Status != StatusApproved || approved.Response != "ok" {
		t.Fatalf("approved request = %+v", approved)
	}
}

func TestTrackerRejectsAndErrorsForUnknownRequest(t *testing.T) {
	tracker, err := NewTracker(t.TempDir())
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	req, err := tracker.Create("plan_approval", "alice", "lead", map[string]any{"plan": "edit file"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	rejected, err := tracker.Reject(req.ID, "too risky")
	if err != nil {
		t.Fatalf("Reject returned error: %v", err)
	}
	if rejected.Status != StatusRejected || rejected.Response != "too risky" {
		t.Fatalf("rejected request = %+v", rejected)
	}
	if _, err := tracker.Approve("req-missing", "ok"); err == nil || !strings.Contains(err.Error(), "request not found") {
		t.Fatalf("unknown request error = %v, want request not found", err)
	}
}

func TestProtocolToolsSendRequestsAndResponsesThroughInbox(t *testing.T) {
	root := t.TempDir()
	teamManager, err := team.NewManager(root, nil)
	if err != nil {
		t.Fatalf("team.NewManager returned error: %v", err)
	}
	tracker, err := NewTracker(root)
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	reg := tools.NewRegistry()
	Register(reg, tracker, teamManager, "lead")

	out := reg.Run("shutdown_request", map[string]any{"teammate": "alice", "reason": "done for now"})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"id": "req-000001"`) {
		t.Fatalf("shutdown_request output = %q", out)
	}
	inbox, err := teamManager.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox alice returned error: %v", err)
	}
	if len(inbox) != 1 || !strings.Contains(inbox[0].Content, `"kind":"shutdown"`) {
		t.Fatalf("alice inbox = %+v, want shutdown protocol JSON", inbox)
	}

	out = reg.Run("shutdown_response", map[string]any{"request_id": "req-000001", "approve": true, "reason": "stopping"})
	if strings.HasPrefix(out, "Error:") || !strings.Contains(out, `"status": "approved"`) {
		t.Fatalf("shutdown_response output = %q", out)
	}
	leadInbox, err := teamManager.ReadInbox("lead")
	if err != nil {
		t.Fatalf("ReadInbox lead returned error: %v", err)
	}
	if len(leadInbox) != 1 || !strings.Contains(leadInbox[0].Content, `"status":"approved"`) {
		t.Fatalf("lead inbox = %+v, want approved protocol response", leadInbox)
	}
}
