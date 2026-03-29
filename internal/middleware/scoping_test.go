package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listHandler(stacks []map[string]any) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		data, _ := json.Marshal(stacks)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func TestStackScoping_FilterListResult(t *testing.T) {
	scoping := NewStackScoping("myapp")

	stacks := []map[string]any{
		{"id": float64(1), "name": "myapp", "status": "active"},
		{"id": float64(2), "name": "otherapp", "status": "active"},
		{"id": float64(3), "name": "myapp", "status": "inactive"},
	}

	mw := scoping.ForTool("listLocalStacks")
	handler := mw(listHandler(stacks))

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	var filtered []map[string]any
	err = json.Unmarshal([]byte(text), &filtered)
	require.NoError(t, err)

	assert.Len(t, filtered, 2)
	for _, s := range filtered {
		assert.Equal(t, "myapp", s["name"])
	}
}

func TestStackScoping_FilterListResult_NoMatches(t *testing.T) {
	scoping := NewStackScoping("myapp")

	stacks := []map[string]any{
		{"id": float64(1), "name": "otherapp", "status": "active"},
	}

	mw := scoping.ForTool("listLocalStacks")
	handler := mw(listHandler(stacks))

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Equal(t, "[]", text)
}

func TestStackScoping_ValidateStackID_Correct(t *testing.T) {
	scoping := NewStackScoping("myapp")
	scoping.SetStackID(42)

	mw := scoping.ForTool("updateLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"id": float64(42),
	}))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestStackScoping_ValidateStackID_Wrong(t *testing.T) {
	scoping := NewStackScoping("myapp")
	scoping.SetStackID(42)

	mw := scoping.ForTool("updateLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"id": float64(99),
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "Operation rejected")
	assert.Contains(t, text, "99")
	assert.Contains(t, text, "42")
}

func TestStackScoping_ValidateStackID_NotResolved(t *testing.T) {
	scoping := NewStackScoping("myapp")
	// Don't set stack ID

	mw := scoping.ForTool("stopLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"id": float64(1),
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "has not been created yet")
}

func TestStackScoping_CreateLocalStack_CorrectName(t *testing.T) {
	scoping := NewStackScoping("myapp")

	createHandler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("Local stack created successfully with ID: 55"), nil
	}

	mw := scoping.ForTool("createLocalStack")
	handler := mw(createHandler)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "myapp",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "55")

	// Stack ID should now be resolved
	id, resolved := scoping.StackID()
	assert.True(t, resolved)
	assert.Equal(t, 55, id)
}

func TestStackScoping_CreateLocalStack_WrongName(t *testing.T) {
	scoping := NewStackScoping("myapp")

	mw := scoping.ForTool("createLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "wrongapp",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "Cannot create stack")
	assert.Contains(t, text, "scoped to stack 'myapp'")
}

func TestStackScoping_CreateLocalStack_AlreadyExists(t *testing.T) {
	scoping := NewStackScoping("myapp")
	scoping.SetStackID(42)

	mw := scoping.ForTool("createLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"name": "myapp",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "already exists")
}

func TestStackScoping_UnrelatedToolPassthrough(t *testing.T) {
	scoping := NewStackScoping("myapp")

	mw := scoping.ForTool("dockerProxy")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestStackScoping_AllValidatedTools(t *testing.T) {
	tools := []string{"updateLocalStack", "startLocalStack", "stopLocalStack", "deleteLocalStack", "getLocalStackFile"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			scoping := NewStackScoping("myapp")
			scoping.SetStackID(10)

			mw := scoping.ForTool(tool)
			handler := mw(passthroughHandler())

			// Correct ID passes
			result, err := handler(context.Background(), makeRequest(map[string]any{
				"id": float64(10),
			}))
			require.NoError(t, err)
			assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)

			// Wrong ID is rejected
			result, err = handler(context.Background(), makeRequest(map[string]any{
				"id": float64(99),
			}))
			require.NoError(t, err)
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "Operation rejected")
		})
	}
}
