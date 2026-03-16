package middleware

import (
	"context"
	"strings"
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

func TestTierGating_DevPassthrough(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	// Even a destructive tool should pass through in dev tier
	mw := NewTierGating(TierDev, store, "deleteLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestTierGating_ProdBlocksWithoutToken(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	mw := NewTierGating(TierProd, store, "deleteLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "Production operation requires approval")
	assert.Contains(t, text, "deleteLocalStack")
}

func TestTierGating_ProdPassesWithValidToken(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	toolName := "createLocalStack"

	// First call: get a token
	mw := NewTierGating(TierProd, store, toolName)
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "approvalToken")

	// Extract token from response
	// Format: ...approvalToken: "abcdef01"
	tokenStart := strings.Index(text, `approvalToken: "`) + len(`approvalToken: "`)
	tokenEnd := strings.Index(text[tokenStart:], `"`)
	token := text[tokenStart : tokenStart+tokenEnd]

	// Second call: use the token
	result, err = handler(context.Background(), makeRequest(map[string]any{
		"approvalToken": token,
	}))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestTierGating_ProdInvalidToken(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	mw := NewTierGating(TierProd, store, "deleteLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"approvalToken": "invalid-token",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "Invalid or expired approval token")
}

func TestTierGating_ProdReadToolPassthrough(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	// Read tools should pass through even in prod
	mw := NewTierGating(TierProd, store, "listLocalStacks")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestTierGating_ProdOperationalPassthrough(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	mw := NewTierGating(TierProd, store, "startLocalStack")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestTierGating_DockerProxyGETPassthrough(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	mw := NewTierGating(TierProd, store, "dockerProxy")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"method": "GET",
	}))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestTierGating_DockerProxyPOSTRequiresApproval(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	mw := NewTierGating(TierProd, store, "dockerProxy")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"method": "POST",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "Production operation requires approval")
}

func TestTierGating_UnknownToolDefaultsToConfiguration(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	// Unknown tools default to CategoryConfiguration, which needs approval in prod
	mw := NewTierGating(TierProd, store, "unknownTool")
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(nil))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "Production operation requires approval")
}
