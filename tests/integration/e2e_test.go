package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/towline"
	"github.com/changethisusername/towline/pkg/portainer/models"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildMiddlewareChain creates the full middleware chain for a given tool name,
// matching the production setup in cmd/towline-mcp/main.go.
func buildMiddlewareChain(
	tier middleware.Tier,
	approvalStore *approval.Store,
	scoping *middleware.StackScoping,
	ownership *middleware.ContainerOwnership,
	toolName string,
) []middleware.MiddlewareFunc {
	var mws []middleware.MiddlewareFunc

	// Outermost: tier gating (runs first)
	mws = append(mws, middleware.NewTierGating(tier, approvalStore, toolName))

	// Stack scoping
	mws = append(mws, scoping.ForTool(toolName))

	// Container ownership (only for dockerProxy)
	if toolName == "dockerProxy" {
		mws = append(mws, ownership.ForDockerProxy())
	}

	return mws
}

// wrapHandler applies the full middleware chain to a handler.
func wrapHandler(
	handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error),
	tier middleware.Tier,
	approvalStore *approval.Store,
	scoping *middleware.StackScoping,
	ownership *middleware.ContainerOwnership,
	toolName string,
) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	mws := buildMiddlewareChain(tier, approvalStore, scoping, ownership, toolName)
	return middleware.Chain(handler, mws...)
}

// newTestEnv creates a full test environment with all middleware and a mock client.
func newTestEnv(t *testing.T, tier middleware.Tier, mock *mockPortainerClient) *testEnv {
	t.Helper()

	toolsPath := writeTempToolsYAML(t)
	towlineToolsPath := writeTempTowlineToolsYAML(t)

	srv, err := mcp.NewPortainerMCPServer(
		"http://mock.local", "test-token", toolsPath,
		mcp.WithClient(mock),
		mcp.WithDisableVersionCheck(true),
	)
	require.NoError(t, err)

	towlineTools, err := loadTowlineTools(towlineToolsPath)
	require.NoError(t, err)
	srv.MergeTools(towlineTools)

	approvalStore := approval.NewStore()
	t.Cleanup(func() { approvalStore.Stop() })

	scoping := middleware.NewStackScoping("testapp-dev")
	scoping.SetStackID(1)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return mock.proxyDockerRequestRaw(opts)
	}

	ownership := middleware.NewContainerOwnership("testapp-dev", 1, proxyFn)

	handlers := towline.NewHandlers(srv, "testapp-dev", 1, proxyFn)

	return &testEnv{
		srv:           srv,
		approvalStore: approvalStore,
		scoping:       scoping,
		ownership:     ownership,
		handlers:      handlers,
		mock:          mock,
		tier:          tier,
	}
}

type testEnv struct {
	srv           *mcp.PortainerMCPServer
	approvalStore *approval.Store
	scoping       *middleware.StackScoping
	ownership     *middleware.ContainerOwnership
	handlers      *towline.Handlers
	mock          *mockPortainerClient
	tier          middleware.Tier
}

// wrap applies the full middleware chain to a handler for a specific tool name.
func (e *testEnv) wrap(toolName string, handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return wrapHandler(handler, e.tier, e.approvalStore, e.scoping, e.ownership, toolName)
}

// call invokes a wrapped handler with the given arguments.
func (e *testEnv) call(t *testing.T, toolName string, handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error), args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	wrapped := e.wrap(toolName, handler)
	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: args,
		},
	}
	result, err := wrapped(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// resultText extracts the text content from a tool result.
func resultText(t *testing.T, result *mcpgo.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(mcpgo.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

// TestE2E_DevTier_FullAccess verifies that in dev tier, an updateLocalStack call
// passes through all middleware without requiring approval.
func TestE2E_DevTier_FullAccess(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleUpdateLocalStack()
	result := env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "updated successfully")
	assert.True(t, mock.updateLocalStackCalled, "expected UpdateLocalStack to be called")
}

// TestE2E_ProdTier_RequiresApproval verifies that in prod tier, updateLocalStack
// returns an approval token message, and re-calling with the token executes the handler.
func TestE2E_ProdTier_RequiresApproval(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.srv.HandleUpdateLocalStack()

	// First call: should return approval request
	result := env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "Production operation requires approval")
	assert.Contains(t, text, "approvalToken")
	assert.False(t, mock.updateLocalStackCalled, "handler should not have been called yet")

	// Extract token from response
	token := extractApprovalToken(t, text)

	// Second call with token: should execute
	result = env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
		"approvalToken": token,
	})

	text = resultText(t, result)
	assert.Contains(t, text, "updated successfully")
	assert.True(t, mock.updateLocalStackCalled)
}

// TestE2E_ProdTier_ReadToolNoApproval verifies that in prod tier,
// read-category tools like towline_service_health pass through without approval.
func TestE2E_ProdTier_ReadToolNoApproval(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.handlers.HandleServiceHealth()
	result := env.call(t, "towline_service_health", handler, map[string]any{})

	text := resultText(t, result)
	// Should not contain approval message
	assert.NotContains(t, text, "approval")
	// Should be valid JSON (health result)
	assert.True(t, json.Valid([]byte(text)), "expected valid JSON, got: %s", text)
}

// TestE2E_StackScoping_RejectsWrongStack verifies that an updateLocalStack call
// with the wrong stack ID is rejected by scoping middleware.
func TestE2E_StackScoping_RejectsWrongStack(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleUpdateLocalStack()
	result := env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(999),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "does not match scoped stack")
	assert.True(t, result.IsError)
	assert.False(t, mock.updateLocalStackCalled)
}

// TestE2E_StackScoping_FiltersListResults verifies that a listLocalStacks call
// returns only the scoped stack, even if the mock returns multiple stacks.
func TestE2E_StackScoping_FiltersListResults(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleGetLocalStacks()
	result := env.call(t, "listLocalStacks", handler, map[string]any{})

	text := resultText(t, result)
	var stacks []map[string]any
	err := json.Unmarshal([]byte(text), &stacks)
	require.NoError(t, err)

	// Should only contain the scoped stack
	require.Len(t, stacks, 1)
	assert.Equal(t, "testapp-dev", stacks[0]["name"])
}

// TestE2E_ContainerOwnership_BlocksWrongStack verifies that a dockerProxy call
// targeting a container not in the scoped stack is rejected.
func TestE2E_ContainerOwnership_BlocksWrongStack(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleDockerProxy()
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "GET",
		"dockerAPIPath": "/containers/foreign-container-id/logs",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "does not belong to stack")
	assert.True(t, result.IsError)
}

// TestE2E_ContainerOwnership_AllowsOwnedContainer verifies that a dockerProxy call
// targeting a container in the scoped stack passes through.
func TestE2E_ContainerOwnership_AllowsOwnedContainer(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleDockerProxy()
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "GET",
		"dockerAPIPath": "/containers/owned-container-abc123/logs",
	})

	text := resultText(t, result)
	assert.NotContains(t, text, "does not belong")
	assert.False(t, result.IsError)
}

// TestE2E_TowlineHealth_ThroughMiddleware verifies towline_service_health called
// through the full middleware chain in dev tier returns health data.
func TestE2E_TowlineHealth_ThroughMiddleware(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.handlers.HandleServiceHealth()
	result := env.call(t, "towline_service_health", handler, map[string]any{})

	text := resultText(t, result)
	var health []map[string]any
	err := json.Unmarshal([]byte(text), &health)
	require.NoError(t, err)
	require.NotEmpty(t, health)
	assert.Equal(t, "web", health[0]["service"])
	assert.Equal(t, "running", health[0]["state"])
}

// TestE2E_TowlineEnvSet_ProdApproval verifies towline_env_set in prod tier requires
// approval and works after providing the token.
func TestE2E_TowlineEnvSet_ProdApproval(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.handlers.HandleEnvSet()

	// First call: should require approval
	result := env.call(t, "towline_env_set", handler, map[string]any{
		"name":  "APP_PORT",
		"value": "8080",
	})
	text := resultText(t, result)
	assert.Contains(t, text, "Production operation requires approval")

	token := extractApprovalToken(t, text)

	// Second call with token
	result = env.call(t, "towline_env_set", handler, map[string]any{
		"name":          "APP_PORT",
		"value":         "8080",
		"approvalToken": token,
	})
	text = resultText(t, result)
	assert.Contains(t, text, "set successfully")
	assert.True(t, mock.updateLocalStackCalled)
}

// TestE2E_FullDeploymentCycle tests the common agent workflow in dev tier:
// get compose file -> update it -> verify service health.
func TestE2E_FullDeploymentCycle(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	// Step 1: Get compose file
	getFileHandler := env.srv.HandleGetLocalStackFile()
	result := env.call(t, "getLocalStackFile", getFileHandler, map[string]any{
		"id": float64(1),
	})
	composeContent := resultText(t, result)
	assert.Contains(t, composeContent, "services")

	// Step 2: Update it
	updateHandler := env.srv.HandleUpdateLocalStack()
	updatedCompose := "version: '3'\nservices:\n  web:\n    image: nginx:latest\n"
	result = env.call(t, "updateLocalStack", updateHandler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          updatedCompose,
	})
	text := resultText(t, result)
	assert.Contains(t, text, "updated successfully")

	// Step 3: Verify service health
	healthHandler := env.handlers.HandleServiceHealth()
	result = env.call(t, "towline_service_health", healthHandler, map[string]any{})
	text = resultText(t, result)

	var health []map[string]any
	err := json.Unmarshal([]byte(text), &health)
	require.NoError(t, err)
	require.NotEmpty(t, health)
	assert.Equal(t, "running", health[0]["state"])
}

// extractApprovalToken extracts the approval token from a tier gating response message.
func extractApprovalToken(t *testing.T, text string) string {
	t.Helper()
	// The format is: ...approvalToken: "XXXXXXXX"
	idx := strings.Index(text, `approvalToken: "`)
	require.NotEqual(t, -1, idx, "could not find approvalToken in: %s", text)
	start := idx + len(`approvalToken: "`)
	end := strings.Index(text[start:], `"`)
	require.NotEqual(t, -1, end, "could not find closing quote for approvalToken")
	token := text[start : start+end]
	require.NotEmpty(t, token)
	return token
}
