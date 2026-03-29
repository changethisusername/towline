package middleware

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewEnvFilter returns a middleware that strips _TOWLINE_ prefixed environment
// variables from createLocalStack and updateLocalStack requests. This prevents
// agents from overriding internal towline state stored in stack env vars.
//
// This filtering is implemented as middleware (rather than in the upstream
// handler) to keep the upstream local_stack.go handler file unmodified for
// clean mergeability with future Portainer MCP releases.
func NewEnvFilter(toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		if toolName != "createLocalStack" && toolName != "updateLocalStack" {
			return next
		}
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := request.GetArguments()
			if args == nil {
				return next(ctx, request)
			}

			env, ok := args["env"].([]interface{})
			if !ok || env == nil {
				return next(ctx, request)
			}

			filtered := make([]interface{}, 0, len(env))
			for _, item := range env {
				obj, ok := item.(map[string]interface{})
				if !ok {
					filtered = append(filtered, item)
					continue
				}
				name, _ := obj["name"].(string)
				if strings.HasPrefix(name, "_TOWLINE_") {
					continue
				}
				filtered = append(filtered, item)
			}
			args["env"] = filtered

			return next(ctx, request)
		}
	}
}
