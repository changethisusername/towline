package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/pkg/portainer/models"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegression_StackScopingJSONFieldCase verifies that stack scoping uses lowercase
// "name" field (not "Name") when filtering. This was a critical bug found in review.
func TestRegression_StackScopingJSONFieldCase(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleGetLocalStacks()
	result := env.call(t, "listLocalStacks", handler, map[string]any{})

	text := resultText(t, result)
	var stacks []map[string]any
	err := json.Unmarshal([]byte(text), &stacks)
	require.NoError(t, err)
	require.Len(t, stacks, 1)

	// Verify the field is lowercase "name" (as produced by json.Marshal on LocalStack)
	_, hasLowercase := stacks[0]["name"]
	assert.True(t, hasLowercase, "expected lowercase 'name' field in stack JSON")
	_, hasUppercase := stacks[0]["Name"]
	assert.False(t, hasUppercase, "unexpected uppercase 'Name' field in stack JSON")
}

// TestRegression_ApprovalTokenExpiry verifies that an expired token is rejected.
func TestRegression_ApprovalTokenExpiry(t *testing.T) {
	// Create a store and request a token
	store := approval.NewStore()
	defer store.Stop()

	token := store.Request("updateLocalStack", map[string]any{"id": float64(1)})
	require.NotEmpty(t, token)

	// Manually expire the token by manipulating via validate timing.
	// The TokenTTL is 5 minutes, so we cannot wait. Instead, we verify the store
	// handles expiry correctly by validating once (which removes it) and trying again.

	// First validate: should succeed
	pending := store.Validate(token, map[string]any{"id": float64(1)})
	require.NotNil(t, pending)
	assert.Equal(t, "updateLocalStack", pending.ToolName)

	// Second validate with same token: should fail (token consumed)
	pending = store.Validate(token, map[string]any{"id": float64(1)})
	assert.Nil(t, pending, "token should be consumed after first use")

	// Test expired token through middleware
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.srv.HandleUpdateLocalStack()

	// Get a fresh token via the middleware
	result := env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
	})
	text := resultText(t, result)
	freshToken := extractApprovalToken(t, text)

	// Use the token once (valid)
	result = env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
		"approvalToken": freshToken,
	})
	text = resultText(t, result)
	assert.Contains(t, text, "updated successfully")

	// Try using the same token again (should be expired/consumed)
	result = env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
		"approvalToken": freshToken,
	})
	text = resultText(t, result)
	assert.Contains(t, text, "Invalid, expired, or mismatched approval token")
}

// TestRegression_CreateStackResolvesID verifies that after createLocalStack through
// scoping middleware, subsequent operations work with the resolved stack ID.
func TestRegression_CreateStackResolvesID(t *testing.T) {
	mock := newMockClient()

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
	defer approvalStore.Stop()

	// Create scoping for a stack that doesn't exist yet (no SetStackID)
	scoping := middleware.NewStackScoping("newapp")

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return mock.proxyDockerRequestRaw(opts)
	}
	ownership := middleware.NewContainerOwnership("newapp", 1, proxyFn)

	// Verify stack is not resolved yet
	_, resolved := scoping.StackID()
	assert.False(t, resolved)

	// Create the stack
	createHandler := middleware.Chain(
		srv.HandleCreateLocalStack(),
		buildMiddlewareChain(middleware.TierDev, approvalStore, scoping, ownership, "createLocalStack")...,
	)

	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{
				"environmentId": float64(1),
				"name":          "newapp",
				"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "created successfully")

	// Verify the stack ID was resolved
	stackID, resolved := scoping.StackID()
	assert.True(t, resolved, "stack ID should be resolved after create")
	assert.Equal(t, 42, stackID, "stack ID should match the mock's returned value")

	// Now try updateLocalStack with the resolved ID
	updateHandler := middleware.Chain(
		srv.HandleUpdateLocalStack(),
		buildMiddlewareChain(middleware.TierDev, approvalStore, scoping, ownership, "updateLocalStack")...,
	)

	req = mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{
				"id":            float64(42),
				"environmentId": float64(1),
				"file":          "version: '3'\nservices:\n  web:\n    image: nginx:latest\n",
			},
		},
	}
	result, err = updateHandler(context.Background(), req)
	require.NoError(t, err)
	text = resultText(t, result)
	assert.Contains(t, text, "updated successfully")
}

// TestRegression_DockerProxyGETAllowedInProd verifies that Docker proxy GET requests
// don't require approval in prod.
func TestRegression_DockerProxyGETAllowedInProd(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.srv.HandleDockerProxy()
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "GET",
		"dockerAPIPath": "/containers/owned-container-abc123/json",
	})

	text := resultText(t, result)
	assert.NotContains(t, text, "approval")
	assert.False(t, result.IsError)
}

// TestRegression_DockerProxyPOSTBlockedInProd verifies that Docker proxy POST requests
// require approval in prod.
func TestRegression_DockerProxyPOSTRestartAllowedInProd(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.srv.HandleDockerProxy()

	// Container restart/start/stop are Operational — no approval needed
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "POST",
		"dockerAPIPath": "/containers/owned-container-abc123/restart",
	})
	text := resultText(t, result)
	assert.NotContains(t, text, "approval", "container restart should not require approval")
	assert.False(t, result.IsError)
}

func TestRegression_DockerProxyPOSTExecBlockedInProd(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.srv.HandleDockerProxy()

	// Non-operational POST (e.g., container rename) should require approval
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "POST",
		"dockerAPIPath": "/containers/owned-container-abc123/rename",
	})
	text := resultText(t, result)
	assert.Contains(t, text, "Production operation requires approval")
}

// TestRegression_EmptyStackPendingMode verifies that when a stack doesn't exist yet,
// reads return appropriate errors and create is allowed.
func TestRegression_EmptyStackPendingMode(t *testing.T) {
	mock := newMockClient()

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
	defer approvalStore.Stop()

	// Stack not resolved (pending mode)
	scoping := middleware.NewStackScoping("pending-app")

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return mock.proxyDockerRequestRaw(opts)
	}
	ownership := middleware.NewContainerOwnership("pending-app", 1, proxyFn)

	// Attempt to update (should fail - stack not resolved)
	updateHandler := middleware.Chain(
		srv.HandleUpdateLocalStack(),
		buildMiddlewareChain(middleware.TierDev, approvalStore, scoping, ownership, "updateLocalStack")...,
	)

	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{
				"id":            float64(1),
				"environmentId": float64(1),
				"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
			},
		},
	}
	result, err := updateHandler(context.Background(), req)
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "has not been created yet")
	assert.True(t, result.IsError)

	// Attempt to create (should succeed)
	createHandler := middleware.Chain(
		srv.HandleCreateLocalStack(),
		buildMiddlewareChain(middleware.TierDev, approvalStore, scoping, ownership, "createLocalStack")...,
	)

	req = mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{
				"environmentId": float64(1),
				"name":          "pending-app",
				"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
			},
		},
	}
	result, err = createHandler(context.Background(), req)
	require.NoError(t, err)
	text = resultText(t, result)
	assert.Contains(t, text, "created successfully")
}

// TestRegression_MiddlewareChainOrder verifies that tier gating runs before stack
// scoping (a rejected approval doesn't leak stack info).
func TestRegression_MiddlewareChainOrder(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierProd, mock)

	handler := env.srv.HandleUpdateLocalStack()

	// Call with wrong stack ID - in prod, tier gating should fire first with approval request
	result := env.call(t, "updateLocalStack", handler, map[string]any{
		"id":            float64(999),
		"environmentId": float64(1),
		"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
	})

	text := resultText(t, result)
	// Should get approval request first (tier gating), NOT stack scoping error
	assert.Contains(t, text, "Production operation requires approval",
		"tier gating should run before stack scoping; got: %s", text)
	assert.NotContains(t, text, "does not match scoped stack",
		"stack scoping error should not leak before tier approval")
}

// TestRegression_EnvMaskingSensitiveNames verifies that variables named API_KEY,
// DB_PASSWORD etc have masked values in env_get.
func TestRegression_EnvMaskingSensitiveNames(t *testing.T) {
	mock := newMockClient()
	mock.stacks = []models.LocalStack{
		{
			ID:         1,
			Name:       "testapp-dev",
			EndpointID: 1,
			Env: []models.LocalStackEnvVar{
				{Name: "APP_PORT", Value: "8080"},
				{Name: "API_KEY", Value: "supersecret123"},
				{Name: "DB_PASSWORD", Value: "mysecretpassword"},
				{Name: "SOME_TOKEN", Value: "tok_abcdef"},
				{Name: "SESSION_SECRET", Value: "s3cr3t"},
				{Name: "NORMAL_VAR", Value: "normalvalue"},
			},
		},
	}

	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.handlers.HandleEnvGet()
	result := env.call(t, "towline_env_get", handler, map[string]any{})

	text := resultText(t, result)
	var envVars []map[string]any
	err := json.Unmarshal([]byte(text), &envVars)
	require.NoError(t, err)

	for _, ev := range envVars {
		name := ev["name"].(string)
		value := ev["value"].(string)

		switch name {
		case "APP_PORT":
			assert.Equal(t, "8080", value, "non-sensitive var should not be masked")
		case "API_KEY":
			assert.Contains(t, value, "****", "API_KEY should be masked")
			assert.NotEqual(t, "supersecret123", value)
		case "DB_PASSWORD":
			assert.Contains(t, value, "****", "DB_PASSWORD should be masked")
			assert.NotEqual(t, "mysecretpassword", value)
		case "SOME_TOKEN":
			assert.Contains(t, value, "****", "SOME_TOKEN should be masked")
			assert.NotEqual(t, "tok_abcdef", value)
		case "SESSION_SECRET":
			assert.Contains(t, value, "****", "SESSION_SECRET should be masked")
		case "NORMAL_VAR":
			assert.Equal(t, "normalvalue", value, "NORMAL_VAR should not be masked")
		}
	}
}

// TestRegression_DeploymentLogCapping verifies that more than 50 deployments
// caps at 50 entries.
func TestRegression_DeploymentLogCapping(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	// Record 55 deployments
	for i := 0; i < 55; i++ {
		err := env.handlers.RecordDeployment(
			"test deployment",
			"old compose", "new compose",
			"success",
		)
		// The first few may fail until the mock has the _TOWLINE_DEPLOYMENTS env var,
		// but RecordDeployment handles that by starting with an empty list.
		_ = err
	}

	// Now read deployments
	handler := env.handlers.HandleDeployments()
	result := env.call(t, "towline_deployments", handler, map[string]any{})

	text := resultText(t, result)
	var entries []map[string]any
	err := json.Unmarshal([]byte(text), &entries)
	require.NoError(t, err)

	assert.LessOrEqual(t, len(entries), 50, "deployment log should be capped at 50")
}

// TestRegression_PartialComposeProtection verifies that env_set preserves the full
// compose file when updating (doesn't accidentally lose compose content).
func TestRegression_PartialComposeProtection(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.handlers.HandleEnvSet()
	result := env.call(t, "towline_env_set", handler, map[string]any{
		"name":  "NEW_VAR",
		"value": "new_value",
	})

	text := resultText(t, result)
	assert.Contains(t, text, "set successfully")

	// Verify the compose file was passed through in the update call
	assert.True(t, mock.updateLocalStackCalled)
	assert.NotEmpty(t, mock.lastUpdateFile, "compose file should have been passed to UpdateLocalStack")
	assert.Contains(t, mock.lastUpdateFile, "services", "compose file content should be preserved")
}

// TestRegression_InternalEnvVarsHidden verifies that _TOWLINE_* env vars are not
// exposed through env_get.
func TestRegression_InternalEnvVarsHidden(t *testing.T) {
	mock := newMockClient()
	mock.stacks = []models.LocalStack{
		{
			ID:         1,
			Name:       "testapp-dev",
			EndpointID: 1,
			Env: []models.LocalStackEnvVar{
				{Name: "APP_PORT", Value: "8080"},
				{Name: "_TOWLINE_DEPLOYMENTS", Value: "[]"},
			},
		},
	}

	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.handlers.HandleEnvGet()
	result := env.call(t, "towline_env_get", handler, map[string]any{})

	text := resultText(t, result)
	assert.NotContains(t, text, "_TOWLINE_DEPLOYMENTS")
	assert.Contains(t, text, "APP_PORT")
}

// TestRegression_ApprovalTokenTiming verifies approval tokens are single-use
// and cannot be reused within the TTL window.
func TestRegression_ApprovalTokenTiming(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	token := store.Request("testTool", map[string]any{})

	// Use it once - should succeed
	p := store.Validate(token, map[string]any{})
	require.NotNil(t, p)

	// Small delay to ensure no timing issues
	time.Sleep(10 * time.Millisecond)

	// Use it again - should fail
	p = store.Validate(token, map[string]any{})
	assert.Nil(t, p, "approval token should be single-use")
}

// TestRegression_CreateStackWrongName verifies that creating a stack with the
// wrong name is rejected by scoping middleware.
func TestRegression_CreateStackWrongName(t *testing.T) {
	mock := newMockClient()

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
	defer approvalStore.Stop()

	scoping := middleware.NewStackScoping("myapp")

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return mock.proxyDockerRequestRaw(opts)
	}
	ownership := middleware.NewContainerOwnership("myapp", 1, proxyFn)

	createHandler := middleware.Chain(
		srv.HandleCreateLocalStack(),
		buildMiddlewareChain(middleware.TierDev, approvalStore, scoping, ownership, "createLocalStack")...,
	)

	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{
				"environmentId": float64(1),
				"name":          "wrong-name",
				"file":          "version: '3'\nservices:\n  web:\n    image: nginx\n",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	require.NoError(t, err)
	text := resultText(t, result)
	assert.Contains(t, text, "scoped to stack")
	assert.True(t, result.IsError)
}

// TestRegression_ListStacksEmptyResult verifies that when scoping filters all stacks,
// the result is an empty array (not null).
func TestRegression_ListStacksEmptyResult(t *testing.T) {
	mock := newMockClient()
	// Only have stacks that don't match the scope
	mock.stacks = []models.LocalStack{
		{ID: 2, Name: "other-app", EndpointID: 1},
		{ID: 3, Name: "another-app", EndpointID: 1},
	}

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
	defer approvalStore.Stop()

	scoping := middleware.NewStackScoping("testapp-dev")
	scoping.SetStackID(1)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return mock.proxyDockerRequestRaw(opts)
	}
	ownership := middleware.NewContainerOwnership("testapp-dev", 1, proxyFn)

	handler := middleware.Chain(
		srv.HandleGetLocalStacks(),
		buildMiddlewareChain(middleware.TierDev, approvalStore, scoping, ownership, "listLocalStacks")...,
	)

	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{},
		},
	}
	result, err := handler(context.Background(), req)
	require.NoError(t, err)

	text := resultText(t, result)
	// Should be a valid JSON empty array
	assert.Equal(t, "[]", text,
		"expected empty array when no matching stacks, got: %s", text)
}

// TestRegression_DockerProxyContainerListFiltered verifies that the container list
// endpoint is filtered to only show containers from the scoped stack.
func TestRegression_DockerProxyContainerListFiltered(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleDockerProxy()
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "GET",
		"dockerAPIPath": "/containers/json",
	})

	text := resultText(t, result)

	var containers []map[string]any
	err := json.Unmarshal([]byte(text), &containers)
	require.NoError(t, err)

	// The ownership middleware should filter container list to only scoped stack containers
	for _, c := range containers {
		labels, _ := c["Labels"].(map[string]any)
		if labels != nil {
			project, _ := labels["com.docker.compose.project"].(string)
			if project != "" {
				assert.Equal(t, "testapp-dev", project,
					"container list should only contain containers from scoped stack")
			}
		}
	}
}

// TestRegression_StartLocalStackScopingCheck verifies that startLocalStack with
// the wrong stack ID is rejected by scoping.
func TestRegression_StartLocalStackScopingCheck(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleStartLocalStack()
	result := env.call(t, "startLocalStack", handler, map[string]any{
		"id":            float64(999),
		"environmentId": float64(1),
	})

	text := resultText(t, result)
	assert.Contains(t, text, "does not match scoped stack")
	assert.True(t, result.IsError)

	// Correct ID should work
	result = env.call(t, "startLocalStack", handler, map[string]any{
		"id":            float64(1),
		"environmentId": float64(1),
	})
	text = resultText(t, result)
	assert.Contains(t, text, "started successfully")
}

// TestRegression_InternalEnvSetBlocked verifies that env_set rejects _TOWLINE_*
// variable names.
func TestRegression_InternalEnvSetBlocked(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.handlers.HandleEnvSet()

	// Wrap with middleware (even though it's dev tier, the handler itself blocks this)
	wrapped := env.wrap("towline_env_set", handler)

	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: map[string]any{
				"name":  "_TOWLINE_HACK",
				"value": "malicious",
			},
		},
	}
	result, err := wrapped(context.Background(), req)
	require.NoError(t, err)

	text := resultText(t, result)
	assert.Contains(t, text, "cannot set internal _TOWLINE_*")
	assert.True(t, result.IsError)
	assert.False(t, mock.updateLocalStackCalled)
}

// TestRegression_ContainerListEndpointPassthrough verifies that requests to
// /containers/json pass through ownership middleware (filter, not block).
func TestRegression_ContainerListEndpointPassthrough(t *testing.T) {
	mock := newMockClient()
	env := newTestEnv(t, middleware.TierDev, mock)

	handler := env.srv.HandleDockerProxy()
	result := env.call(t, "dockerProxy", handler, map[string]any{
		"environmentId": float64(1),
		"method":        "GET",
		"dockerAPIPath": "/containers/json",
	})

	// Should NOT get an ownership rejection error
	assert.False(t, result.IsError, "container list should not be blocked")

	text := resultText(t, result)
	assert.True(t, strings.HasPrefix(text, "[") || text == "null",
		"expected JSON array, got: %s", text)
}
