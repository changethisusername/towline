package middleware

import (
	"context"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalTokenFromResult(t *testing.T) {
	approver := &fakeApprover{status: approval.StatusPending}
	gate := newGate(t, approver)
	handler := NewTierGating(TierProd, gate, "updateLocalStack")(passthroughHandler())
	args := map[string]any{"id": 1, "file": "services: {}\n"}

	// First call: the request message.
	res, err := handler(context.Background(), makeRequest(args))
	require.NoError(t, err)
	token, ok := ApprovalTokenFromResult(res)
	require.True(t, ok)
	assert.Equal(t, extractToken(t, resultText(res)), token)

	// Re-call while pending: the still-pending message quotes it the same way.
	withToken := map[string]any{"id": 1, "file": "services: {}\n", "approvalToken": token}
	res, err = handler(context.Background(), makeRequest(withToken))
	require.NoError(t, err)
	got, ok := ApprovalTokenFromResult(res)
	require.True(t, ok)
	assert.Equal(t, token, got)

	// The remote-gateway variant of the message.
	gate.ApprovalURL = "https://mcp.example.com/ui"
	res, err = handler(context.Background(), makeRequest(map[string]any{"id": 2}))
	require.NoError(t, err)
	_, ok = ApprovalTokenFromResult(res)
	assert.True(t, ok)

	for name, r := range map[string]*mcp.CallToolResult{
		"nil":          nil,
		"error result": mcp.NewToolResultError(`approvalToken: "0123456789abcdef0123456789abcdef"`),
		"no token":     mcp.NewToolResultText("Local stack updated successfully"),
		"short token":  mcp.NewToolResultText(`approvalToken: "0123"`),
		"not hex":      mcp.NewToolResultText(`approvalToken: "zz23456789abcdef0123456789abcdef"`),
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := ApprovalTokenFromResult(r)
			assert.False(t, ok)
		})
	}
}

func TestTierGating_OriginDetailsInApprovalRequest(t *testing.T) {
	approver := &fakeApprover{status: approval.StatusPending}
	handler := NewTierGating(TierProd, newGate(t, approver), "updateLocalStack")(passthroughHandler())
	ctx := WithCaller(context.Background(), Caller{ID: "autodeploy:shop", Scope: ScopeDeploy, Origin: &DeployOrigin{
		Details: "Auto-deploy of a new commit on acme/shop\nCommit: " + sha40,
	}})
	_, err := handler(ctx, makeRequest(map[string]any{"id": 1, "file": "services: {}\n"}))
	require.NoError(t, err)
	require.Len(t, approver.submitted, 1)
	req := approver.submitted[0]
	assert.Contains(t, req.Description, "Commit: "+sha40)
	assert.Contains(t, req.Description, "Arguments:\n{")
	assert.Equal(t, "autodeploy:shop", req.Client)

	// Without an origin the description is the arguments only.
	_, err = handler(context.Background(), makeRequest(map[string]any{"id": 1, "file": "x"}))
	require.NoError(t, err)
	assert.NotContains(t, approver.submitted[1].Description, "Arguments:")
}

const sha40 = "0123456789abcdef0123456789abcdef01234567"

func TestEnvFilter_Origin(t *testing.T) {
	var gotDescription string
	prepare := func(_, description string) ([]models.LocalStackEnvVar, []models.LocalStackEnvVar, error) {
		gotDescription = description
		return []models.LocalStackEnvVar{{Name: "GIT_SHA", Value: "old"}, {Name: "OTHER", Value: "1"}},
			[]models.LocalStackEnvVar{{Name: "_TOWLINE_DEPLOYMENTS", Value: "[]"}}, nil
	}
	envOf := func(env []interface{}) map[string]string {
		m := map[string]string{}
		for _, e := range env {
			o := e.(map[string]interface{})
			m[o["name"].(string)] = o["value"].(string)
		}
		return m
	}

	tests := []struct {
		name     string
		caller   *Caller
		wantDesc string
		wantEnv  map[string]string
		wantErr  bool
	}{
		{
			name:     "agent update keeps the default description",
			wantDesc: "Stack updated via updateLocalStack",
			wantEnv:  map[string]string{"GIT_SHA": "old", "OTHER": "1", "_TOWLINE_DEPLOYMENTS": "[]"},
		},
		{
			name:     "auto-deploy summary and commit env",
			caller:   &Caller{ID: "autodeploy:shop", Scope: ScopeDeploy, Origin: &DeployOrigin{Summary: "Auto-deploy acme/shop@0123456: fix", Commit: sha40, CommitEnv: "GIT_SHA"}},
			wantDesc: "Auto-deploy acme/shop@0123456: fix",
			wantEnv:  map[string]string{"GIT_SHA": sha40, "OTHER": "1", "_TOWLINE_DEPLOYMENTS": "[]"},
		},
		{
			name:     "auto-deploy without commit env",
			caller:   &Caller{ID: "autodeploy:shop", Scope: ScopeDeploy, Origin: &DeployOrigin{Summary: "Auto-deploy x", Commit: sha40}},
			wantDesc: "Auto-deploy x",
			wantEnv:  map[string]string{"GIT_SHA": "old", "OTHER": "1", "_TOWLINE_DEPLOYMENTS": "[]"},
		},
		{
			name:    "internal commit env refused",
			caller:  &Caller{ID: "autodeploy:shop", Origin: &DeployOrigin{Summary: "x", Commit: sha40, CommitEnv: "_TOWLINE_DEPLOYMENTS"}},
			wantErr: true,
		},
		{
			name:    "non-sha commit refused",
			caller:  &Caller{ID: "autodeploy:shop", Origin: &DeployOrigin{Summary: "x", Commit: "main\nEVIL=1", CommitEnv: "GIT_SHA"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDescription = ""
			ctx := context.Background()
			if tt.caller != nil {
				ctx = WithCaller(ctx, *tt.caller)
			}
			var got []interface{}
			called := false
			handler := NewEnvFilter("updateLocalStack", prepare)(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				return captureEnv(&got)(ctx, req)
			})
			res, err := handler(ctx, makeRequest(map[string]any{"file": "services: {}\n"}))
			require.NoError(t, err)
			if tt.wantErr {
				assert.True(t, res.IsError)
				assert.False(t, called)
				return
			}
			assert.Equal(t, tt.wantDesc, gotDescription)
			assert.Equal(t, tt.wantEnv, envOf(got))
		})
	}
}
