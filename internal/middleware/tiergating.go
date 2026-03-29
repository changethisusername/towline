package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// operationalDockerPaths are container lifecycle actions that do not require
// approval in prod tier (per CLAUDE.md "Operational" category).
var operationalDockerPaths = []string{"/restart", "/start", "/stop"}

// NewTierGating returns a middleware that gates tool calls based on tier.
func NewTierGating(tier Tier, store *approval.Store, toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tier == TierDev {
				return next(ctx, request)
			}

			category := CategoryForTool(toolName)

			// Docker proxy: non-GET needs approval, except container lifecycle ops
			if toolName == "dockerProxy" {
				parser := toolgen.NewParameterParser(request)
				method, _ := parser.GetString("method", false)
				if method != "" && method != "GET" {
					path, _ := parser.GetString("dockerAPIPath", false)
					if isOperationalDockerPath(path) {
						category = CategoryOperational
					} else {
						category = CategoryExec
					}
				}
			}

			if !category.NeedsApproval() {
				return next(ctx, request)
			}

			// Check for approval token
			parser := toolgen.NewParameterParser(request)
			token, _ := parser.GetString("approvalToken", false)

			if token != "" {
				pending := store.Validate(token, request.GetArguments())
				if pending == nil {
					return mcp.NewToolResultError("Invalid, expired, or mismatched approval token. The arguments must match the original request. Request a new one by calling this tool without the approvalToken parameter."), nil
				}
				if pending.ToolName != toolName {
					return mcp.NewToolResultError(fmt.Sprintf("Approval token was issued for %q, not %q. Request a new token for this tool.", pending.ToolName, toolName)), nil
				}
				return next(ctx, request)
			}

			newToken := store.Request(toolName, request.GetArguments())
			msg := fmt.Sprintf(
				"Production operation requires approval.\nAction: %s\nTo confirm, re-call this tool with the additional parameter approvalToken: \"%s\"",
				toolName, newToken,
			)
			return mcp.NewToolResultText(msg), nil
		}
	}
}

// isOperationalDockerPath returns true if the Docker API path is a container
// lifecycle action (restart, start, stop) that is classified as Operational.
func isOperationalDockerPath(path string) bool {
	for _, suffix := range operationalDockerPaths {
		if strings.HasSuffix(path, suffix) && strings.HasPrefix(path, "/containers/") {
			return true
		}
	}
	return false
}
