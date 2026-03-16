package middleware

import (
	"context"
	"fmt"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewTierGating returns a middleware that gates tool calls based on tier.
func NewTierGating(tier Tier, store *approval.Store, toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tier == TierDev {
				return next(ctx, request)
			}

			category := CategoryForTool(toolName)

			// Docker proxy non-GET needs approval
			if toolName == "dockerProxy" {
				parser := toolgen.NewParameterParser(request)
				method, _ := parser.GetString("method", false)
				if method != "" && method != "GET" {
					category = CategoryExec
				}
			}

			if !category.NeedsApproval() {
				return next(ctx, request)
			}

			// Check for approval token
			parser := toolgen.NewParameterParser(request)
			token, _ := parser.GetString("approvalToken", false)

			if token != "" {
				pending := store.Validate(token)
				if pending == nil {
					return mcp.NewToolResultError("Invalid or expired approval token. Request a new one by calling this tool without the approvalToken parameter."), nil
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
