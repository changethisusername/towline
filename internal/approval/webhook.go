package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Status is the state of an approval request on the approval server.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Request is the payload POSTed to the approval webhook.
type Request struct {
	ID           string `json:"id"`
	Project      string `json:"project"`
	Action       string `json:"action"`
	Description  string `json:"description"`
	StackContent string `json:"stackContent,omitempty"`
}

// Approver submits approval requests to a human and reports their decision.
type Approver interface {
	Submit(ctx context.Context, req Request) error
	Status(ctx context.Context, id string) (Status, error)
}

// Webhook is an Approver backed by an external approval server implementing:
//
//	POST /      — receive approval request
//	GET  /{id}  — poll status ({"status": "pending"|"approved"|"rejected"})
//
// The server is responsible for authenticating whoever approves or rejects;
// the agent must not be able to reach its approve/reject endpoints.
type Webhook struct {
	baseURL   string
	authToken string
	client    *http.Client
}

// NewWebhook creates a Webhook approver. If authToken is non-empty it is sent
// as a bearer token on every request.
func NewWebhook(baseURL, authToken string) (*Webhook, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid approval webhook URL %q", baseURL)
	}
	return &Webhook{
		baseURL:   strings.TrimRight(baseURL, "/"),
		authToken: authToken,
		client:    &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Submit sends a new approval request.
func (w *Webhook) Submit(ctx context.Context, req Request) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal approval request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+"/", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create approval request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := w.do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// Status polls the decision for a request.
func (w *Webhook) Status(ctx context.Context, id string) (Status, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, w.baseURL+"/"+url.PathEscape(id), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create status request: %w", err)
	}
	resp, err := w.do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Status Status `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("failed to parse approval status: %w", err)
	}
	switch out.Status {
	case StatusPending, StatusApproved, StatusRejected:
		return out.Status, nil
	}
	return "", fmt.Errorf("approval server returned unknown status %q", out.Status)
}

func (w *Webhook) do(req *http.Request) (*http.Response, error) {
	if w.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+w.authToken)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach approval server: %w", err)
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("approval server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}
