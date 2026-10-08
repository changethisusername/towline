package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
)

func TestTierGating_TokenBoundToCaller(t *testing.T) {
	for _, mode := range []ApprovalMode{ApprovalHuman, ApprovalAgent} {
		t.Run(string(mode), func(t *testing.T) {
			approver := &fakeApprover{status: approval.StatusApproved}
			gate := newGate(t, approver)
			gate.Mode = mode
			handler := NewTierGating(TierProd, gate, "updateLocalStack")(passthroughHandler())
			alice := WithCaller(context.Background(), Caller{ID: "conn_alice", Scope: ScopeDeploy})
			bob := WithCaller(context.Background(), Caller{ID: "conn_bob", Scope: ScopeDeploy})

			args := map[string]any{"id": 1.0, "file": "services: {}"}
			res, _ := handler(alice, makeRequest(args))
			token := extractToken(t, resultText(res))
			if mode == ApprovalHuman && approver.submitted[0].Client != "conn_alice" {
				t.Fatalf("approval request should name the caller, got %q", approver.submitted[0].Client)
			}

			withToken := map[string]any{"id": 1.0, "file": "services: {}", "approvalToken": token}
			res, _ = handler(bob, makeRequest(withToken))
			if !res.IsError || !strings.Contains(resultText(res), "different client") {
				t.Fatalf("another caller used the token: %s", resultText(res))
			}
			if mode == ApprovalHuman {
				// The token is released, so its own caller can still use it.
				res, _ = handler(alice, makeRequest(withToken))
				if res.IsError {
					t.Fatalf("own caller refused: %s", resultText(res))
				}
			}
		})
	}
}

func TestCallerGate(t *testing.T) {
	read := WithCaller(context.Background(), Caller{ID: "c", Scope: ScopeRead})
	deploy := WithCaller(context.Background(), Caller{ID: "c", Scope: ScopeDeploy})
	cases := []struct {
		name    string
		ctx     context.Context
		tool    string
		args    map[string]any
		require bool
		allowed bool
	}{
		{"stdio has no caller", context.Background(), "updateLocalStack", nil, false, true},
		{"remote without caller fails closed", context.Background(), "towline_service_health", nil, true, false},
		{"read scope reads", read, "towline_service_logs", nil, true, true},
		{"read scope cannot deploy", read, "updateLocalStack", nil, true, false},
		{"read scope cannot scale", read, "towline_scale", nil, true, false},
		{"read scope docker GET", read, "dockerProxy", map[string]any{"method": "GET"}, true, true},
		{"read scope docker POST", read, "dockerProxy", map[string]any{"method": "POST"}, true, false},
		{"read scope unknown tool", read, "somethingNew", nil, true, false},
		{"deploy scope deploys", deploy, "deleteLocalStack", nil, true, true},
		{"unknown scope", WithCaller(context.Background(), Caller{ID: "c", Scope: "admin"}), "towline_service_health", nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := NewCallerGate(tc.tool, tc.require)(passthroughHandler())(tc.ctx, makeRequest(tc.args))
			if res.IsError == tc.allowed {
				t.Fatalf("allowed=%v, got %s", tc.allowed, resultText(res))
			}
		})
	}
}
