package middleware

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

func passthroughHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("executed"), nil
	}
}

// fakeApprover records submitted requests and returns a configurable status.
type fakeApprover struct {
	mu        sync.Mutex
	submitted []approval.Request
	status    approval.Status
	submitErr error
}

func (f *fakeApprover) Submit(ctx context.Context, req approval.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return f.submitErr
	}
	f.submitted = append(f.submitted, req)
	return nil
}

func (f *fakeApprover) Status(ctx context.Context, id string) (approval.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.submitted {
		if r.ID == id {
			return f.status, nil
		}
	}
	return "", errors.New("unknown id")
}

func newGate(t *testing.T, approver approval.Approver) *ApprovalGate {
	store := approval.NewStore()
	t.Cleanup(store.Stop)
	return &ApprovalGate{Store: store, Approver: approver, Project: "myapp-prod"}
}

func resultText(r *mcp.CallToolResult) string {
	return r.Content[0].(mcp.TextContent).Text
}

func extractToken(t *testing.T, text string) string {
	t.Helper()
	marker := `approvalToken: "`
	i := strings.Index(text, marker)
	require.GreaterOrEqual(t, i, 0, "no token in %q", text)
	rest := text[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

func TestTierGating_DevPassthrough(t *testing.T) {
	// Even a destructive tool should pass through in dev tier, with no approver.
	handler := NewTierGating(TierDev, nil, "deleteLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "executed", resultText(result))
}

func TestTierGating_ProdWithoutApproverFailsClosed(t *testing.T) {
	handler := NewTierGating(TierProd, newGate(t, nil), "deleteLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(result), "no approval webhook is configured")
}

func TestTierGating_ProdSubmitsRequest(t *testing.T) {
	approver := &fakeApprover{status: approval.StatusPending}
	handler := NewTierGating(TierProd, newGate(t, approver), "updateLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"id":   1,
		"file": "services: {}\n",
	}))
	require.NoError(t, err)
	text := resultText(result)
	assert.Contains(t, text, "requires human approval")

	require.Len(t, approver.submitted, 1)
	req := approver.submitted[0]
	assert.Equal(t, extractToken(t, text), req.ID)
	assert.Equal(t, "myapp-prod", req.Project)
	assert.Equal(t, "updateLocalStack", req.Action)
	assert.Equal(t, "services: {}\n", req.StackContent)
	assert.NotContains(t, req.Description, "services")
}

func TestTierGating_ProdTokenAloneDoesNotAuthorize(t *testing.T) {
	approver := &fakeApprover{status: approval.StatusPending}
	handler := NewTierGating(TierProd, newGate(t, approver), "deleteLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	token := extractToken(t, resultText(result))

	// Re-calling immediately with the token must not execute.
	result, err = handler(context.Background(), makeRequest(map[string]any{"approvalToken": token}))
	require.NoError(t, err)
	assert.Contains(t, resultText(result), "still pending")

	// Rejected: refused, and token consumed.
	approver.status = approval.StatusRejected
	result, err = handler(context.Background(), makeRequest(map[string]any{"approvalToken": token}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(result), "rejected")

	approver.status = approval.StatusApproved
	result, err = handler(context.Background(), makeRequest(map[string]any{"approvalToken": token}))
	require.NoError(t, err)
	assert.Contains(t, resultText(result), "Invalid, expired, or mismatched")
}

func TestTierGating_ProdPassesWhenApproved(t *testing.T) {
	approver := &fakeApprover{status: approval.StatusApproved}
	handler := NewTierGating(TierProd, newGate(t, approver), "createLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	token := extractToken(t, resultText(result))

	result, err = handler(context.Background(), makeRequest(map[string]any{"approvalToken": token}))
	require.NoError(t, err)
	assert.Equal(t, "executed", resultText(result))

	// Tokens are single use.
	result, err = handler(context.Background(), makeRequest(map[string]any{"approvalToken": token}))
	require.NoError(t, err)
	assert.Contains(t, resultText(result), "Invalid, expired, or mismatched")
}

func TestTierGating_ProdArgsMustMatch(t *testing.T) {
	approver := &fakeApprover{status: approval.StatusApproved}
	handler := NewTierGating(TierProd, newGate(t, approver), "towline_env_set")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{"name": "A", "value": "1"}))
	require.NoError(t, err)
	token := extractToken(t, resultText(result))

	result, err = handler(context.Background(), makeRequest(map[string]any{"name": "A", "value": "2", "approvalToken": token}))
	require.NoError(t, err)
	assert.Contains(t, resultText(result), "Invalid, expired, or mismatched")
}

func TestTierGating_ProdSubmitFailure(t *testing.T) {
	approver := &fakeApprover{submitErr: errors.New("connection refused")}
	handler := NewTierGating(TierProd, newGate(t, approver), "deleteLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(result), "failed to submit approval request")
}

func TestTierGating_ProdInvalidToken(t *testing.T) {
	handler := NewTierGating(TierProd, newGate(t, &fakeApprover{}), "deleteLocalStack")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"approvalToken": "invalid-token",
	}))
	require.NoError(t, err)
	assert.Contains(t, resultText(result), "Invalid, expired, or mismatched approval token")
}

func TestTierGating_ProdTokenBoundToTool(t *testing.T) {
	gate := newGate(t, &fakeApprover{status: approval.StatusApproved})

	deleteHandler := NewTierGating(TierProd, gate, "deleteLocalStack")(passthroughHandler())
	result, err := deleteHandler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	token := extractToken(t, resultText(result))

	updateHandler := NewTierGating(TierProd, gate, "updateLocalStack")(passthroughHandler())
	result, err = updateHandler(context.Background(), makeRequest(map[string]any{
		"approvalToken": token,
	}))
	require.NoError(t, err)

	text := resultText(result)
	assert.Contains(t, text, "was issued for")
	assert.Contains(t, text, "deleteLocalStack")
}

func TestTierGating_ProdNoApprovalNeeded(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"read tool", "listLocalStacks", nil},
		{"operational tool", "startLocalStack", nil},
		{"docker GET", "dockerProxy", map[string]any{"method": "GET", "dockerAPIPath": "/containers/json"}},
		{"docker restart", "dockerProxy", map[string]any{"method": "POST", "dockerAPIPath": "/containers/abc/restart"}},
		{"docker versioned stop", "dockerProxy", map[string]any{"method": "POST", "dockerAPIPath": "/v1.43/containers/abc/stop"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No approver: anything that needed approval would fail closed.
			handler := NewTierGating(TierProd, newGate(t, nil), tt.tool)(passthroughHandler())
			result, err := handler(context.Background(), makeRequest(tt.args))
			require.NoError(t, err)
			assert.Equal(t, "executed", resultText(result))
		})
	}
}

func TestTierGating_ProdDockerProxyNeedsApproval(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{"POST without path", map[string]any{"method": "POST"}},
		{"container delete", map[string]any{"method": "DELETE", "dockerAPIPath": "/containers/abc"}},
		{"container kill", map[string]any{"method": "POST", "dockerAPIPath": "/containers/abc/kill"}},
		{"container create", map[string]any{"method": "POST", "dockerAPIPath": "/containers/create"}},
		{"query string disguised as restart", map[string]any{"method": "POST", "dockerAPIPath": "/containers/create?x=/containers/a/restart"}},
		{"reserved id restart", map[string]any{"method": "POST", "dockerAPIPath": "/containers/json/restart"}},
		{"lowercase method", map[string]any{"method": "get", "dockerAPIPath": "/containers/json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewTierGating(TierProd, newGate(t, nil), "dockerProxy")(passthroughHandler())
			result, err := handler(context.Background(), makeRequest(tt.args))
			require.NoError(t, err)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(result), "requires human approval")
		})
	}
}

func TestTierGating_UnknownToolDefaultsToConfiguration(t *testing.T) {
	// Unknown tools default to CategoryConfiguration, which needs approval in prod
	handler := NewTierGating(TierProd, newGate(t, nil), "unknownTool")(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Contains(t, resultText(result), "requires human approval")
}
