package protocols

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"learn-claude-code-go/internal/team"
	"learn-claude-code-go/internal/tools"
)

const (
	KindShutdown     = "shutdown"
	KindPlanApproval = "plan_approval"

	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Request 是团队协议里的可追踪请求。
// 普通 inbox 消息只表达“告诉你一件事”；Request 额外带 ID 和状态，方便把请求、审批、拒绝配对审计。
type Request struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Status    string         `json:"status"`
	Payload   map[string]any `json:"payload"`
	Response  string         `json:"response,omitempty"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type store struct {
	NextID   int       `json:"next_id"`
	Requests []Request `json:"requests"`
}

// Tracker 用本地状态机管理 request-response 生命周期。
// 它把状态保存到 .team/requests.json，让协议状态能被 CLI 查看，也能跨进程恢复。
type Tracker struct {
	mu       sync.Mutex
	path     string
	nextID   int
	requests map[string]Request
}

func NewTracker(root string) (*Tracker, error) {
	tracker := &Tracker{
		path:     filepath.Join(root, ".team", "requests.json"),
		nextID:   1,
		requests: map[string]Request{},
	}
	if err := tracker.load(); err != nil {
		return nil, err
	}
	return tracker, nil
}

func (t *Tracker) Create(kind, from, to string, payload map[string]any) (Request, error) {
	kind = strings.TrimSpace(kind)
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if err := validateKind(kind); err != nil {
		return Request{}, err
	}
	if from == "" {
		from = "lead"
	}
	if to == "" {
		return Request{}, fmt.Errorf("to is required")
	}
	now := time.Now().Format(time.RFC3339)

	t.mu.Lock()
	defer t.mu.Unlock()
	req := Request{
		ID:        fmt.Sprintf("req-%06d", t.nextID),
		Kind:      kind,
		From:      from,
		To:        to,
		Status:    StatusPending,
		Payload:   payload,
		CreatedAt: now,
		UpdatedAt: now,
	}
	t.nextID++
	t.requests[req.ID] = req
	if err := t.persistLocked(); err != nil {
		return Request{}, err
	}
	return req, nil
}

func (t *Tracker) Approve(id, response string) (Request, error) {
	return t.transition(id, StatusApproved, response)
}

func (t *Tracker) Reject(id, response string) (Request, error) {
	return t.transition(id, StatusRejected, response)
}

func (t *Tracker) Get(id string) (Request, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	req, ok := t.requests[strings.TrimSpace(id)]
	if !ok {
		return Request{}, fmt.Errorf("request not found: %s", id)
	}
	return req, nil
}

func (t *Tracker) List() []Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return sortedRequests(t.requests)
}

func (t *Tracker) transition(id, status, response string) (Request, error) {
	id = strings.TrimSpace(id)
	response = strings.TrimSpace(response)

	t.mu.Lock()
	defer t.mu.Unlock()
	req, ok := t.requests[id]
	if !ok {
		return Request{}, fmt.Errorf("request not found: %s", id)
	}
	if req.Status != StatusPending && req.Status != status {
		return Request{}, fmt.Errorf("invalid request transition %s -> %s", req.Status, status)
	}
	req.Status = status
	req.Response = response
	req.UpdatedAt = time.Now().Format(time.RFC3339)
	t.requests[id] = req
	if err := t.persistLocked(); err != nil {
		return Request{}, err
	}
	return req, nil
}

func (t *Tracker) load() error {
	data, err := os.ReadFile(t.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved store
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if saved.NextID > 0 {
		t.nextID = saved.NextID
	}
	for _, req := range saved.Requests {
		if req.ID == "" {
			continue
		}
		t.requests[req.ID] = req
	}
	return nil
}

func (t *Tracker) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store{NextID: t.nextID, Requests: sortedRequests(t.requests)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(t.path, data, 0o644)
}

func validateKind(kind string) error {
	switch kind {
	case KindShutdown, KindPlanApproval:
		return nil
	default:
		return fmt.Errorf("unsupported request kind %q", kind)
	}
}

func sortedRequests(requests map[string]Request) []Request {
	out := make([]Request, 0, len(requests))
	for _, req := range requests {
		out = append(out, req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func Register(reg *tools.Registry, tracker *Tracker, teamManager *team.Manager, sender string) {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		sender = "lead"
	}
	reg.Register(tools.Tool{
		Spec: tools.Spec("shutdown_request", "Request a teammate to gracefully shut down.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"teammate": map[string]any{"type": "string"},
				"reason":   map[string]any{"type": "string"},
			},
			"required": []string{"teammate"},
		}),
		Handler: func(input map[string]any) (string, error) {
			to := stringValue(input, "teammate")
			req, err := tracker.Create(KindShutdown, sender, to, map[string]any{"reason": stringValue(input, "reason")})
			if err != nil {
				return "", err
			}
			if err := sendProtocol(teamManager, sender, to, "protocol_request", req); err != nil {
				return "", err
			}
			return jsonOut(req, nil)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("shutdown_response", "Approve or reject a shutdown request.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request_id": map[string]any{"type": "string"},
				"approve":    map[string]any{"type": "boolean"},
				"reason":     map[string]any{"type": "string"},
			},
			"required": []string{"request_id", "approve"},
		}),
		Handler: func(input map[string]any) (string, error) {
			req, err := reviewRequest(tracker, stringValue(input, "request_id"), boolValue(input, "approve"), stringValue(input, "reason"))
			if err != nil {
				return "", err
			}
			if req.Kind == KindShutdown && req.Status == StatusApproved {
				_ = teamManager.Stop(req.To)
			}
			if err := sendProtocol(teamManager, sender, req.From, "protocol_response", req); err != nil {
				return "", err
			}
			return jsonOut(req, nil)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("plan_submit", "Submit a plan for approval before risky work.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"plan": map[string]any{"type": "string"}},
			"required":   []string{"plan"},
		}),
		Handler: func(input map[string]any) (string, error) {
			req, err := tracker.Create(KindPlanApproval, sender, "lead", map[string]any{"plan": stringValue(input, "plan")})
			if err != nil {
				return "", err
			}
			if err := sendProtocol(teamManager, sender, "lead", "protocol_request", req); err != nil {
				return "", err
			}
			return jsonOut(req, nil)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("plan_review", "Approve or reject a submitted plan.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request_id": map[string]any{"type": "string"},
				"approve":    map[string]any{"type": "boolean"},
				"feedback":   map[string]any{"type": "string"},
			},
			"required": []string{"request_id", "approve"},
		}),
		Handler: func(input map[string]any) (string, error) {
			req, err := reviewRequest(tracker, stringValue(input, "request_id"), boolValue(input, "approve"), stringValue(input, "feedback"))
			if err != nil {
				return "", err
			}
			if err := sendProtocol(teamManager, sender, req.From, "protocol_response", req); err != nil {
				return "", err
			}
			return jsonOut(req, nil)
		},
	})
	reg.Register(tools.Tool{
		Spec: tools.Spec("request_status", "List all team protocol requests and statuses.", map[string]any{"type": "object", "properties": map[string]any{}}),
		Handler: func(input map[string]any) (string, error) {
			return jsonOut(map[string]any{"requests": tracker.List()}, nil)
		},
	})
}

func reviewRequest(tracker *Tracker, id string, approve bool, response string) (Request, error) {
	if approve {
		return tracker.Approve(id, response)
	}
	return tracker.Reject(id, response)
}

func sendProtocol(teamManager *team.Manager, from, to, event string, req Request) error {
	body, err := json.Marshal(map[string]any{
		"type":    event,
		"request": req,
	})
	if err != nil {
		return err
	}
	return teamManager.Send(from, to, string(body))
}

func jsonOut(value any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func stringValue(input map[string]any, name string) string {
	value, ok := input[name]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func boolValue(input map[string]any, name string) bool {
	value, ok := input[name]
	if !ok {
		return false
	}
	b, _ := value.(bool)
	return b
}
