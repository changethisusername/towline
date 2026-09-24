package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/middleware"
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
	gate *middleware.ApprovalGate,
	scoping *middleware.StackScoping,
	ownership *middleware.ContainerOwnership,
	prepare middleware.StackUpdatePreparer,
	toolName string,
) []middleware.MiddlewareFunc {
	var mws []middleware.MiddlewareFunc

	// Outermost: compose security policy
	mws = append(mws, middleware.NewComposePolicy(toolName, middleware.ComposePolicy{}))

	// Tier gating
	mws = append(mws, middleware.NewTierGating(tier, gate, toolName))

	// Stack scoping
	mws = append(mws, scoping.ForTool(toolName))

	// Env filter
	mws = append(mws, middleware.NewEnvFilter(toolName, prepare))

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
	gate *middleware.ApprovalGate,
	scoping *middleware.StackScoping,
	ownership *middleware.ContainerOwnership,
	prepare middleware.StackUpdatePreparer,
	toolName string,
) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	mws := buildMiddlewareChain(tier, gate, scoping, ownership, prepare, toolName)
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

	gate, approver := newTestGate(t)

	scoping := middleware.NewStackScoping("testapp-dev")
	scoping.SetStackID(1)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return mock.proxyDockerRequestRaw(opts)
	}

	ownership := middleware.NewContainerOwnership("testapp-dev", 1, proxyFn)

	handlers := towline.NewHandlers(srv, "testapp-dev", 1, proxyFn)

	return &testEnv{
		srv:       srv,
		gate:      gate,
		approver:  approver,
		scoping:   scoping,
		ownership: ownership,
		handlers:  handlers,
		mock:      mock,
		tier:      tier,
	}
}

type testEnv struct {
	srv       *mcp.PortainerMCPServer
	gate      *middleware.ApprovalGate
	approver  *testApprover
	scoping   *middleware.StackScoping
	ownership *middleware.ContainerOwnership
	handlers  *towline.Handlers
	mock      *mockPortainerClient
	tier      middleware.Tier
}

// wrap applies the full middleware chain to a handler for a specific tool name.
func (e *testEnv) wrap(toolName string, handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return wrapHandler(handler, e.tier, e.gate, e.scoping, e.ownership, e.handlers.PrepareStackUpdate, toolName)
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
	assert.Contains(t, text, "requires human approval")
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
	assert.Contains(t, text, "requires human approval")

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

// testApprover is an in-memory approval server. Tests set status to simulate
// the human's decision; it defaults to approved.
type testApprover struct {
	mu        sync.Mutex
	status    approval.Status
	submitted []approval.Request
}

func (a *testApprover) Submit(ctx context.Context, req approval.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.submitted = append(a.submitted, req)
	return nil
}

func (a *testApprover) Status(ctx context.Context, id string) (approval.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status, nil
}

func (a *testApprover) set(status approval.Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
}

func newTestGate(t *testing.T) (*middleware.ApprovalGate, *testApprover) {
	t.Helper()
	store := approval.NewStore()
	t.Cleanup(store.Stop)
	approver := &testApprover{status: approval.StatusApproved}
	return &middleware.ApprovalGate{Store: store, Approver: approver, Project: "testapp-dev"}, approver
}

// TestE2E_ProdTier_PendingApprovalBlocks verifies the agent cannot proceed by
// re-calling with the token before a human approves.
func TestE2E_ProdTier_PendingApprovalBlocks(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)
	env.approver.set(approval.StatusPending)

	handler := env.srv.HandleUpdateLocalStack()
	args := map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
	}
	token := extractApprovalToken(t, resultText(t, env.call(t, "updateLocalStack", handler, args)))
	require.Len(t, env.approver.submitted, 1)
	assert.Contains(t, env.approver.submitted[0].StackContent, "nginx")

	args["approvalToken"] = token
	text := resultText(t, env.call(t, "updateLocalStack", handler, args))
	assert.Contains(t, text, "still pending")
	assert.False(t, mock.updateLocalStackCalled)

	env.approver.set(approval.StatusApproved)
	text = resultText(t, env.call(t, "updateLocalStack", handler, args))
	assert.Contains(t, text, "updated successfully")
}

// TestE2E_ComposePolicyRejectsHostEscape verifies a privileged or host-mounting
// compose file never reaches Portainer, even in dev tier.
func TestE2E_ComposePolicyRejectsHostEscape(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	result := env.call(t, "updateLocalStack", env.srv.HandleUpdateLocalStack(), map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "services:\n  pwn:\n    image: alpine\n    privileged: true\n    volumes: [\"/:/host\"]\n",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "security policy")
	assert.False(t, mock.updateLocalStackCalled)
}
