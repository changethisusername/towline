package middleware

import (
	"context"
	"fmt"

	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Scope limits what a remote caller may do with a project.
type Scope string

const (
	// ScopeRead allows observation tools only.
	ScopeRead Scope = "read"
	// ScopeDeploy allows every tool the project's tier allows.
	ScopeDeploy Scope = "deploy"
)

// ParseScope validates a scope name.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeRead, ScopeDeploy:
		return Scope(s), nil
	}
	return "", fmt.Errorf("scope must be %q or %q, got %q", ScopeRead, ScopeDeploy, s)
}

// Caller identifies an authenticated remote client. The stdio server has no
// caller: the agent that spawned it is the only client.
type Caller struct {
	// ID names the client (a client token's name or an OAuth client id).
	ID    string
	Scope Scope
}

type callerKey struct{}

// WithCaller returns a context carrying the authenticated caller.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFromContext returns the authenticated caller, if any.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// callerID is the caller's ID, or "" for the local stdio agent.
func callerID(ctx context.Context) string {
	c, _ := CallerFromContext(ctx)
	return c.ID
}

// IsReadOnlyCall reports whether a tool call has no side effects: an
// observation tool, or a dockerProxy GET/HEAD.
func IsReadOnlyCall(toolName string, request mcp.CallToolRequest) bool {
	if toolName == "dockerProxy" {
		method, _ := toolgen.NewParameterParser(request).GetString("method", false)
		return isReadMethod(method)
	}
	_, known := toolCategories[toolName]
	return known && CategoryForTool(toolName) == CategoryRead
}

// IsReadOnlyTool reports whether a tool can be useful to a read-scoped
// caller (dockerProxy is listed because its GET calls are allowed).
func IsReadOnlyTool(toolName string) bool {
	_, known := toolCategories[toolName]
	return known && CategoryForTool(toolName) == CategoryRead
}

// NewCallerGate enforces the caller's scope. With requireCaller set (remote
// servers), a call without an authenticated caller is refused, so a
// transport that forgot to authenticate fails closed.
func NewCallerGate(toolName string, requireCaller bool) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			c, ok := CallerFromContext(ctx)
			if !ok {
				if requireCaller {
					return mcp.NewToolResultError("unauthenticated call refused"), nil
				}
				return next(ctx, request)
			}
			switch c.Scope {
			case ScopeDeploy:
				return next(ctx, request)
			case ScopeRead:
				if IsReadOnlyCall(toolName, request) {
					return next(ctx, request)
				}
				return mcp.NewToolResultError(fmt.Sprintf("%q changes the project, but this client only has read access. Ask the owner for a client with deploy access.", toolName)), nil
			}
			return mcp.NewToolResultError("unknown client scope; call refused"), nil
		}
	}
}

// ReadScopeToolFilter hides tools a read-scoped caller cannot use from
// tools/list. Calls are still checked by NewCallerGate.
func ReadScopeToolFilter(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	c, ok := CallerFromContext(ctx)
	if !ok || c.Scope == ScopeDeploy {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if IsReadOnlyTool(t.Name) {
			out = append(out, t)
		}
	}
	return out
}
