package middleware

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxDescriptionLen caps the argument summary sent to the approval server.
const maxDescriptionLen = 4000

// ApprovalMode selects who approves prod operations.
type ApprovalMode string

const (
	// ApprovalHuman requires a human decision from the approval server.
	ApprovalHuman ApprovalMode = "human"
	// ApprovalAgent lets the agent operating the MCP confirm its own
	// operations by re-calling with the token (an explicit second step,
	// not a human gate). Intended for autonomous/agentic operation.
	ApprovalAgent ApprovalMode = "agent"
)

// ResolveApprovalMode picks the mode from an explicit setting, falling back
// to the pre-webhook behavior for configurations that predate approval
// modes: human if a webhook is configured, otherwise agent confirmation.
func ResolveApprovalMode(explicit string, webhookConfigured bool) (ApprovalMode, error) {
	switch ApprovalMode(explicit) {
	case ApprovalHuman, ApprovalAgent:
		return ApprovalMode(explicit), nil
	case "":
		if webhookConfigured {
			return ApprovalHuman, nil
		}
		return ApprovalAgent, nil
	}
	return "", fmt.Errorf("approval mode must be %q or %q, got %q", ApprovalHuman, ApprovalAgent, explicit)
}

// ApprovalGate holds what tier gating needs to obtain approval.
// In human mode, Approver may be nil, in which case prod operations that
// need approval are refused (fail closed).
type ApprovalGate struct {
	Mode     ApprovalMode
	Store    *approval.Store
	Approver approval.Approver
	Project  string
}

// NewTierGating returns a middleware that gates tool calls based on tier.
//
// In prod, operations that need approval follow a two-step flow: the first
// call returns a token bound to the tool and its exact arguments, and the
// agent re-calls with the same arguments plus approvalToken.
//
// In human mode the first call also submits a request to the approval
// server, and the re-call only proceeds once the server reports that a
// human approved it; holding the token alone authorizes nothing. In agent
// mode the re-call itself is the confirmation.
func NewTierGating(tier Tier, gate *ApprovalGate, toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tier == TierDev {
				return next(ctx, request)
			}

			category := CategoryForTool(toolName)

			// Docker proxy: mutations need approval, except container lifecycle ops
			parser := toolgen.NewParameterParser(request)
			if toolName == "dockerProxy" {
				method, _ := parser.GetString("method", false)
				if !isReadMethod(method) {
					category = CategoryExec
					rawPath, _ := parser.GetString("dockerAPIPath", false)
					if path, err := NormalizeDockerPath(rawPath); err == nil && isOperationalDockerPath(path, method) {
						category = CategoryOperational
					}
				}
			}

			if !category.NeedsApproval() {
				return next(ctx, request)
			}

			if gate != nil && gate.Mode == ApprovalAgent {
				return agentConfirm(ctx, gate, toolName, request, next)
			}

			if gate == nil || gate.Approver == nil {
				return mcp.NewToolResultError(fmt.Sprintf(
					"Production operation %q requires human approval, but no approval webhook is configured for this MCP server. Ask your human partner to configure -approval-webhook, or to perform this change themselves.",
					toolName)), nil
			}

			args := request.GetArguments()
			token, _ := parser.GetString("approvalToken", false)

			if token == "" {
				return requestApproval(ctx, gate, toolName, args)
			}

			pending := gate.Store.Peek(token, args)
			if pending == nil {
				return mcp.NewToolResultError("Invalid, expired, or mismatched approval token. The arguments must match the original request. Request a new one by calling this tool without the approvalToken parameter."), nil
			}
			if pending.ToolName != toolName {
				return mcp.NewToolResultError(fmt.Sprintf("Approval token was issued for %q, not %q. Request a new token for this tool.", pending.ToolName, toolName)), nil
			}

			status, err := gate.Approver.Status(ctx, token)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("failed to check approval status", err), nil
			}
			switch status {
			case approval.StatusApproved:
				gate.Store.Consume(token)
				return next(ctx, request)
			case approval.StatusRejected:
				gate.Store.Consume(token)
				return mcp.NewToolResultError(fmt.Sprintf("Production operation %q was rejected by a human approver. Do not retry without discussing it with your partner.", toolName)), nil
			default:
				return mcp.NewToolResultText(fmt.Sprintf(
					"Approval for %s is still pending. Wait for your human partner to approve it, then re-call with the same arguments and approvalToken: %q",
					toolName, token)), nil
			}
		}
	}
}

// agentConfirm implements agent mode: the agent confirms its own operation
// by re-calling with the token issued for the exact same arguments.
func agentConfirm(ctx context.Context, gate *ApprovalGate, toolName string, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	token, _ := toolgen.NewParameterParser(request).GetString("approvalToken", false)

	if token == "" {
		newToken := gate.Store.Request(toolName, args)
		return mcp.NewToolResultText(fmt.Sprintf(
			"Production operation requires confirmation.\nAction: %s\nReview what this change does to production. To confirm, re-call this tool with identical arguments plus approvalToken: %q",
			toolName, newToken)), nil
	}

	pending := gate.Store.Validate(token, args)
	if pending == nil {
		return mcp.NewToolResultError("Invalid, expired, or mismatched approval token. The arguments must match the original request. Request a new one by calling this tool without the approvalToken parameter."), nil
	}
	if pending.ToolName != toolName {
		return mcp.NewToolResultError(fmt.Sprintf("Approval token was issued for %q, not %q. Request a new token for this tool.", pending.ToolName, toolName)), nil
	}
	return next(ctx, request)
}

func requestApproval(ctx context.Context, gate *ApprovalGate, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	token := gate.Store.Request(toolName, args)

	req := approval.Request{
		ID:          token,
		Project:     gate.Project,
		Action:      toolName,
		Description: describeArgs(args),
	}
	if file, ok := args["file"].(string); ok {
		req.StackContent = file
	}

	if err := gate.Approver.Submit(ctx, req); err != nil {
		gate.Store.Consume(token)
		return mcp.NewToolResultErrorFromErr("failed to submit approval request", err), nil
	}

	msg := fmt.Sprintf(
		"Production operation requires human approval.\nAction: %s\nAn approval request has been sent (ID %s). Tell your partner what this change does and wait for them to approve it. Then re-call this tool with identical arguments plus approvalToken: %q",
		toolName, token, token,
	)
	return mcp.NewToolResultText(msg), nil
}

// describeArgs summarizes tool arguments for the approver, omitting the
// compose file (sent separately) and the approval token.
func describeArgs(args map[string]any) string {
	filtered := make(map[string]any, len(args))
	for k, v := range args {
		if k == "approvalToken" || k == "file" {
			continue
		}
		filtered[k] = v
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return ""
	}
	s := string(data)
	if len(s) > maxDescriptionLen {
		s = s[:maxDescriptionLen] + "…"
	}
	return s
}
