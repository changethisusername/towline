package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var stackIDValidatedTools = map[string]bool{
	"updateLocalStack":  true,
	"startLocalStack":   true,
	"stopLocalStack":    true,
	"deleteLocalStack":  true,
	"getLocalStackFile": true,
}

var stackListTools = map[string]bool{
	"listLocalStacks": true,
}

// StackScoping enforces that tool calls are scoped to a single stack name.
type StackScoping struct {
	stackName string
	stackID   int
	resolved  bool
	mu        sync.RWMutex
}

// NewStackScoping creates a new StackScoping middleware for the given stack name.
func NewStackScoping(stackName string) *StackScoping {
	return &StackScoping{stackName: stackName}
}

// SetStackID sets the resolved stack ID.
func (s *StackScoping) SetStackID(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stackID = id
	s.resolved = true
}

// StackID returns the resolved stack ID and whether it has been resolved.
func (s *StackScoping) StackID() (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stackID, s.resolved
}

// ForTool returns a middleware function that enforces stack scoping for the given tool.
func (s *StackScoping) ForTool(toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if stackListTools[toolName] {
				return s.filterListResult(ctx, request, next)
			}
			if stackIDValidatedTools[toolName] {
				return s.validateStackID(ctx, request, next, toolName)
			}
			if toolName == "createLocalStack" {
				return s.handleCreate(ctx, request, next)
			}
			return next(ctx, request)
		}
	}
}

func (s *StackScoping) filterListResult(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	result, err := next(ctx, request)
	if err != nil {
		return result, err
	}

	if len(result.Content) == 0 {
		return result, nil
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return result, nil
	}
	text := tc.Text
	var stacks []map[string]any
	if err := json.Unmarshal([]byte(text), &stacks); err != nil {
		return result, nil
	}

	filtered := []map[string]any{}
	for _, stack := range stacks {
		name, _ := stack["name"].(string)
		if name == s.stackName {
			filtered = append(filtered, stack)
		}
	}

	data, _ := json.Marshal(filtered)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *StackScoping) validateStackID(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc, toolName string) (*mcp.CallToolResult, error) {
	id, resolved := s.StackID()
	if !resolved {
		return mcp.NewToolResultError(fmt.Sprintf("Stack '%s' has not been created yet.", s.stackName)), nil
	}

	parser := toolgen.NewParameterParser(request)
	reqID, err := parser.GetInt("id", true)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid id parameter", err), nil
	}

	if reqID != id {
		return mcp.NewToolResultError(fmt.Sprintf("Operation rejected: stack ID %d does not match scoped stack '%s' (ID %d)", reqID, s.stackName, id)), nil
	}

	return next(ctx, request)
}

func (s *StackScoping) handleCreate(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	_, resolved := s.StackID()
	if resolved {
		return mcp.NewToolResultError(fmt.Sprintf("Stack '%s' already exists. Use updateLocalStack to modify it.", s.stackName)), nil
	}

	parser := toolgen.NewParameterParser(request)
	name, _ := parser.GetString("name", true)
	if name != s.stackName {
		return mcp.NewToolResultError(fmt.Sprintf("Cannot create stack '%s': this MCP instance is scoped to stack '%s'", name, s.stackName)), nil
	}

	result, err := next(ctx, request)
	if err != nil {
		return result, err
	}

	// Parse created stack ID from result
	if len(result.Content) == 0 {
		return result, nil
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return result, nil
	}
	text := tc.Text
	var createdID int
	if _, scanErr := fmt.Sscanf(text, "Local stack created successfully with ID: %d", &createdID); scanErr == nil && createdID > 0 {
		s.SetStackID(createdID)
	}

	return result, nil
}
