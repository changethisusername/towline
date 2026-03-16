package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mcpinternal "github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/proxy"
	"github.com/changethisusername/towline/internal/tooldef"
	"github.com/changethisusername/towline/pkg/portainer/models"
)

// ---------- test helpers ----------

const (
	testStackName = "mystack"
	testEnvID     = 1
	testStackID   = 42
)

// setupTestServer creates a PortainerMCPServer backed by a mock client.
func setupTestServer(t *testing.T, client mcpinternal.PortainerClient) *mcpinternal.PortainerMCPServer {
	t.Helper()
	tmpDir := t.TempDir()
	toolsPath := filepath.Join(tmpDir, "tools.yaml")
	tooldef.CreateToolsFileIfNotExists(toolsPath)
	srv, err := mcpinternal.NewPortainerMCPServer("http://fake:9000", "fake-token", toolsPath,
		mcpinternal.WithClient(client),
		mcpinternal.WithDisableVersionCheck(true),
	)
	require.NoError(t, err, "failed to create test server")
	return srv
}

// setupHandlers creates Handlers wired to a mock client and a mock proxy function.
func setupHandlers(t *testing.T, client *mockPortainerClient, proxyFn ProxyFunc) *Handlers {
	t.Helper()
	srv := setupTestServer(t, client)
	return &Handlers{
		Server:    srv,
		StackName: testStackName,
		EnvID:     testEnvID,
		proxyFn:   proxyFn,
	}
}

// newRequest builds a mcp.CallToolRequest with the given arguments.
func newRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

// resultText extracts the text from a successful CallToolResult.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

// defaultStacks returns a minimal stack list for the test stack.
func defaultStacks(env ...models.LocalStackEnvVar) []models.LocalStack {
	return []models.LocalStack{
		{
			ID:         testStackID,
			Name:       testStackName,
			Type:       "compose",
			Status:     "active",
			EndpointID: testEnvID,
			Env:        env,
		},
	}
}

// sampleComposeFile is a minimal compose file used across tests.
const sampleComposeFile = `services:
  web:
    image: nginx:latest
    ports:
      - "80:80"
  db:
    image: postgres:16
`

// containersJSON returns Docker API /containers/json response for the test stack.
func containersJSON(stackName string, containers ...struct {
	id, service, state, status string
}) []byte {
	var list []map[string]any
	for _, c := range containers {
		list = append(list, map[string]any{
			"Id":     c.id,
			"Names":  []string{"/" + stackName + "-" + c.service + "-1"},
			"State":  c.state,
			"Status": c.status,
			"Labels": map[string]string{
				"com.docker.compose.project": stackName,
				"com.docker.compose.service": c.service,
			},
		})
	}
	data, _ := json.Marshal(list)
	return data
}

// statsJSON builds a Docker /containers/{id}/stats response.
func statsJSON(cpuTotal, cpuSystem, preCPUTotal, preCPUSystem, onlineCPUs, memUsage, memLimit float64) []byte {
	data, _ := json.Marshal(map[string]any{
		"cpu_stats": map[string]any{
			"cpu_usage":        map[string]any{"total_usage": cpuTotal},
			"system_cpu_usage": cpuSystem,
			"online_cpus":      onlineCPUs,
		},
		"precpu_stats": map[string]any{
			"cpu_usage":        map[string]any{"total_usage": preCPUTotal},
			"system_cpu_usage": preCPUSystem,
		},
		"memory_stats": map[string]any{
			"usage": memUsage,
			"limit": memLimit,
		},
	})
	return data
}

// inspectJSON builds a Docker /containers/{id}/json response.
func inspectJSON(running bool, startedAt string, restartCount, exitCode int) []byte {
	data, _ := json.Marshal(map[string]any{
		"State": map[string]any{
			"StartedAt":  startedAt,
			"FinishedAt": "0001-01-01T00:00:00Z",
			"ExitCode":   exitCode,
			"Running":    running,
		},
		"RestartCount": restartCount,
	})
	return data
}

// ---------- Health handler tests ----------

func TestHandleServiceHealth_AllServices(t *testing.T) {
	cli := new(mockPortainerClient)

	type container struct {
		id, service, state, status string
	}
	containers := []container{
		{"abc123def456", "web", "running", "Up 2 hours"},
		{"789012ghijkl", "db", "running", "Up 2 hours"},
	}

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{"abc123def456", "web", "running", "Up 2 hours"},
				struct{ id, service, state, status string }{"789012ghijkl", "db", "running", "Up 2 hours"},
			), nil
		}
		for _, c := range containers {
			if opts.Path == fmt.Sprintf("/containers/%s/stats", c.id) {
				return statsJSON(2e9, 2e10, 1e9, 1e10, 4, 104857600, 2147483648), nil
			}
			if opts.Path == fmt.Sprintf("/containers/%s/json", c.id) {
				return inspectJSON(true, "2026-03-17T10:00:00.000000000Z", 0, 0), nil
			}
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleServiceHealth()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	var health []ServiceHealth
	require.NoError(t, json.Unmarshal([]byte(text), &health))
	assert.Len(t, health, 2)
	assert.Equal(t, "web", health[0].Service)
	assert.Equal(t, "running", health[0].State)
	assert.InDelta(t, 40.0, health[0].CpuPercent, 0.01)
	assert.InDelta(t, 100.0, health[0].MemoryUsageMB, 0.01)
	assert.Equal(t, "db", health[1].Service)
}

func TestHandleServiceHealth_SingleService(t *testing.T) {
	cli := new(mockPortainerClient)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{"abc123def456", "web", "running", "Up 2 hours"},
				struct{ id, service, state, status string }{"789012ghijkl", "db", "running", "Up 2 hours"},
			), nil
		}
		if strings.Contains(opts.Path, "/stats") {
			return statsJSON(2e9, 2e10, 1e9, 1e10, 4, 52428800, 1073741824), nil
		}
		if strings.HasSuffix(opts.Path, "/json") && strings.HasPrefix(opts.Path, "/containers/") {
			return inspectJSON(true, "2026-03-17T10:00:00.000000000Z", 1, 0), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleServiceHealth()
	result, err := handler(context.Background(), newRequest(map[string]any{"service": "web"}))
	require.NoError(t, err)

	text := resultText(t, result)
	var health []ServiceHealth
	require.NoError(t, json.Unmarshal([]byte(text), &health))
	assert.Len(t, health, 1)
	assert.Equal(t, "web", health[0].Service)
	assert.Equal(t, 1, health[0].RestartCount)
}

func TestHandleServiceHealth_NoContainers(t *testing.T) {
	cli := new(mockPortainerClient)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return []byte("[]"), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleServiceHealth()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)
	assert.Equal(t, "[]", resultText(t, result))
}

// ---------- Logs handler tests ----------

func TestHandleServiceLogs_Basic(t *testing.T) {
	cli := new(mockPortainerClient)

	// Build a Docker log frame with header
	buildLogFrame := func(payload string) []byte {
		data := []byte(payload)
		frame := make([]byte, 8+len(data))
		frame[0] = 1 // stdout
		size := len(data)
		frame[4] = byte(size >> 24)
		frame[5] = byte(size >> 16)
		frame[6] = byte(size >> 8)
		frame[7] = byte(size)
		copy(frame[8:], data)
		return frame
	}

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{"abc123def456", "web", "running", "Up 2 hours"},
			), nil
		}
		if strings.Contains(opts.Path, "/logs") {
			return buildLogFrame("INFO server started\nERROR connection lost\nINFO recovered\n"), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleServiceLogs()
	result, err := handler(context.Background(), newRequest(map[string]any{"service": "web"}))
	require.NoError(t, err)

	text := resultText(t, result)
	assert.Contains(t, text, "INFO server started")
	assert.Contains(t, text, "ERROR connection lost")
	assert.Contains(t, text, "INFO recovered")
}

func TestHandleServiceLogs_WithFilter(t *testing.T) {
	cli := new(mockPortainerClient)

	buildLogFrame := func(payload string) []byte {
		data := []byte(payload)
		frame := make([]byte, 8+len(data))
		frame[0] = 1
		size := len(data)
		frame[4] = byte(size >> 24)
		frame[5] = byte(size >> 16)
		frame[6] = byte(size >> 8)
		frame[7] = byte(size)
		copy(frame[8:], data)
		return frame
	}

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{"abc123def456", "web", "running", "Up 2 hours"},
			), nil
		}
		if strings.Contains(opts.Path, "/logs") {
			return buildLogFrame("INFO server started\nERROR connection lost\nINFO recovered\n"), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleServiceLogs()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "web",
		"filter":  "ERROR",
	}))
	require.NoError(t, err)

	text := resultText(t, result)
	assert.Contains(t, text, "ERROR connection lost")
	assert.NotContains(t, text, "INFO server started")
	assert.NotContains(t, text, "INFO recovered")
}

func TestHandleServiceLogs_ServiceNotFound(t *testing.T) {
	cli := new(mockPortainerClient)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{"abc123def456", "web", "running", "Up 2 hours"},
			), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleServiceLogs()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "nonexistent",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError, "expected error result for missing service")
}

// ---------- Env handler tests ----------

func TestHandleEnvGet_AllVars(t *testing.T) {
	cli := new(mockPortainerClient)
	stacks := defaultStacks(
		models.LocalStackEnvVar{Name: "DATABASE_URL", Value: "postgres://localhost/db"},
		models.LocalStackEnvVar{Name: "API_KEY", Value: "sk-1234abcd"},
		models.LocalStackEnvVar{Name: "APP_NAME", Value: "myapp"},
	)
	cli.On("GetLocalStacks").Return(stacks, nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleEnvGet()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	var envVars []envVarDisplay
	require.NoError(t, json.Unmarshal([]byte(text), &envVars))

	assert.Len(t, envVars, 3)

	// DATABASE_URL should not be masked
	assert.Equal(t, "DATABASE_URL", envVars[0].Name)
	assert.Equal(t, "postgres://localhost/db", envVars[0].Value)

	// API_KEY should be masked (contains KEY)
	assert.Equal(t, "API_KEY", envVars[1].Name)
	assert.Equal(t, "sk-1****", envVars[1].Value)

	// APP_NAME should not be masked
	assert.Equal(t, "APP_NAME", envVars[2].Name)
	assert.Equal(t, "myapp", envVars[2].Value)

	cli.AssertExpectations(t)
}

func TestHandleEnvGet_HidesTowlineInternal(t *testing.T) {
	cli := new(mockPortainerClient)
	stacks := defaultStacks(
		models.LocalStackEnvVar{Name: "APP_NAME", Value: "myapp"},
		models.LocalStackEnvVar{Name: "_TOWLINE_DEPLOYMENTS", Value: "[]"},
		models.LocalStackEnvVar{Name: "_TOWLINE_PROXY", Value: "traefik"},
	)
	cli.On("GetLocalStacks").Return(stacks, nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleEnvGet()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	var envVars []envVarDisplay
	require.NoError(t, json.Unmarshal([]byte(text), &envVars))

	// Only APP_NAME should be returned; _TOWLINE_* vars should be hidden
	assert.Len(t, envVars, 1)
	assert.Equal(t, "APP_NAME", envVars[0].Name)

	cli.AssertExpectations(t)
}

func TestHandleEnvSet_NewVar(t *testing.T) {
	cli := new(mockPortainerClient)

	stacks := defaultStacks(
		models.LocalStackEnvVar{Name: "EXISTING", Value: "value1"},
	)
	cli.On("GetLocalStacks").Return(stacks, nil)
	cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)
	cli.On("UpdateLocalStack", testStackID, testEnvID, sampleComposeFile, mock.MatchedBy(func(env []models.LocalStackEnvVar) bool {
		if len(env) != 2 {
			return false
		}
		return env[0].Name == "EXISTING" && env[0].Value == "value1" &&
			env[1].Name == "NEW_VAR" && env[1].Value == "new_value"
	}), false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleEnvSet()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"name":  "NEW_VAR",
		"value": "new_value",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, resultText(t, result), "NEW_VAR")
	assert.Contains(t, resultText(t, result), "set successfully")

	cli.AssertExpectations(t)
}

func TestHandleEnvSet_ExistingVar(t *testing.T) {
	cli := new(mockPortainerClient)

	stacks := defaultStacks(
		models.LocalStackEnvVar{Name: "EXISTING", Value: "old_value"},
		models.LocalStackEnvVar{Name: "OTHER", Value: "keep"},
	)
	cli.On("GetLocalStacks").Return(stacks, nil)
	cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)
	cli.On("UpdateLocalStack", testStackID, testEnvID, sampleComposeFile, mock.MatchedBy(func(env []models.LocalStackEnvVar) bool {
		if len(env) != 2 {
			return false
		}
		return env[0].Name == "EXISTING" && env[0].Value == "updated_value" &&
			env[1].Name == "OTHER" && env[1].Value == "keep"
	}), false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleEnvSet()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"name":  "EXISTING",
		"value": "updated_value",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, resultText(t, result), "EXISTING")

	cli.AssertExpectations(t)
}

func TestHandleEnvSet_RejectsTowlineInternal(t *testing.T) {
	cli := new(mockPortainerClient)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleEnvSet()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"name":  "_TOWLINE_DEPLOYMENTS",
		"value": "hacked",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "_TOWLINE_")
}

// ---------- Deployments handler tests ----------

func TestHandleDeployments_Empty(t *testing.T) {
	cli := new(mockPortainerClient)

	stacks := defaultStacks() // no env vars = no deployments
	cli.On("GetLocalStacks").Return(stacks, nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleDeployments()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	var entries []DeploymentEntry
	require.NoError(t, json.Unmarshal([]byte(text), &entries))
	assert.Empty(t, entries)

	cli.AssertExpectations(t)
}

func TestHandleDeployments_WithEntries(t *testing.T) {
	entries := []DeploymentEntry{
		{ID: 1, Timestamp: "2026-03-17T10:00:00Z", Description: "initial deploy", Outcome: "success"},
		{ID: 2, Timestamp: "2026-03-17T11:00:00Z", Description: "add redis", Diff: "+ redis:\n+   image: redis:7\n", Outcome: "success"},
	}
	entriesJSON, _ := json.Marshal(entries)

	cli := new(mockPortainerClient)
	stacks := defaultStacks(
		models.LocalStackEnvVar{Name: "_TOWLINE_DEPLOYMENTS", Value: string(entriesJSON)},
	)
	cli.On("GetLocalStacks").Return(stacks, nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleDeployments()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)

	text := resultText(t, result)
	var returned []DeploymentEntry
	require.NoError(t, json.Unmarshal([]byte(text), &returned))
	assert.Len(t, returned, 2)
	assert.Equal(t, "initial deploy", returned[0].Description)
	assert.Equal(t, "add redis", returned[1].Description)
	assert.Equal(t, "success", returned[1].Outcome)

	cli.AssertExpectations(t)
}

// ---------- Scale handler tests ----------

func TestHandleScale_Basic(t *testing.T) {
	cli := new(mockPortainerClient)

	// getComposeFile calls GetLocalStacks and GetLocalStackFile
	cli.On("GetLocalStacks").Return(defaultStacks(), nil)
	cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)

	// updateComposeFile calls GetLocalStacks again, then UpdateLocalStack
	cli.On("UpdateLocalStack", testStackID, testEnvID, mock.AnythingOfType("string"), mock.Anything, false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleScale()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service":  "web",
		"replicas": float64(3),
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "web")
	assert.Contains(t, text, "3 replicas")
	assert.NotContains(t, text, "Warning")

	cli.AssertExpectations(t)
}

func TestHandleScale_VolumeWarning(t *testing.T) {
	cli := new(mockPortainerClient)

	composeWithVolumes := `services:
  web:
    image: nginx:latest
    volumes:
      - data:/var/lib/data
volumes:
  data:
`

	cli.On("GetLocalStacks").Return(defaultStacks(), nil)
	cli.On("GetLocalStackFile", testStackID).Return(composeWithVolumes, nil)
	cli.On("UpdateLocalStack", testStackID, testEnvID, mock.AnythingOfType("string"), mock.Anything, false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleScale()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service":  "web",
		"replicas": float64(3),
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "Warning")
	assert.Contains(t, text, "volumes")

	cli.AssertExpectations(t)
}

func TestHandleScale_NegativeReplicas(t *testing.T) {
	cli := new(mockPortainerClient)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleScale()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service":  "web",
		"replicas": float64(-1),
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "replicas must be >= 0")
}

// ---------- Exec handler tests ----------

func TestHandleExec_Basic(t *testing.T) {
	cli := new(mockPortainerClient)

	execID := "exec-abc123456789"
	containerID := "abc123def456gh"

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		// ResolveService: list containers
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{containerID, "web", "running", "Up 2 hours"},
			), nil
		}
		// Create exec
		if opts.Path == fmt.Sprintf("/containers/%s/exec", containerID) {
			resp := execCreateResponse{ID: execID}
			data, _ := json.Marshal(resp)
			return data, nil
		}
		// Start exec
		if opts.Path == fmt.Sprintf("/exec/%s/start", execID) {
			return []byte(""), nil
		}
		// Inspect exec (done immediately)
		if opts.Path == fmt.Sprintf("/exec/%s/json", execID) {
			resp := execInspectResponse{Running: false, ExitCode: 0}
			data, _ := json.Marshal(resp)
			return data, nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleExec()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "web",
		"command": "ls -la",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "web")
	assert.Contains(t, text, "Exit code: 0")
}

func TestHandleExec_NonZeroExit(t *testing.T) {
	cli := new(mockPortainerClient)

	execID := "exec-abc123456789"
	containerID := "abc123def456gh"

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{containerID, "web", "running", "Up 2 hours"},
			), nil
		}
		if opts.Path == fmt.Sprintf("/containers/%s/exec", containerID) {
			data, _ := json.Marshal(execCreateResponse{ID: execID})
			return data, nil
		}
		if opts.Path == fmt.Sprintf("/exec/%s/start", execID) {
			return []byte(""), nil
		}
		if opts.Path == fmt.Sprintf("/exec/%s/json", execID) {
			data, _ := json.Marshal(execInspectResponse{Running: false, ExitCode: 127})
			return data, nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleExec()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "web",
		"command": "nonexistent-cmd",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "Exit code: 127")
	assert.Contains(t, text, "non-zero exit code")
}

func TestHandleExec_NoRunningContainer(t *testing.T) {
	cli := new(mockPortainerClient)

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		if opts.Path == "/containers/json" {
			return containersJSON(testStackName,
				struct{ id, service, state, status string }{"abc123def456gh", "web", "exited", "Exited (0)"},
			), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", opts.Path)
	}

	h := setupHandlers(t, cli, proxyFn)
	handler := h.HandleExec()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "web",
		"command": "ls",
	}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "no running container")
}

// ---------- Domains handler tests ----------

func TestHandleDomainsList_Traefik(t *testing.T) {
	cli := new(mockPortainerClient)

	composeWithTraefik := `services:
  web:
    image: nginx:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mystack-web.rule=Host(` + "`" + `example.com` + "`" + `)"
      - "traefik.http.routers.mystack-web.entrypoints=websecure"
      - "traefik.http.routers.mystack-web.tls.certresolver=letsencrypt"
      - "traefik.http.services.mystack-web.loadbalancer.server.port=80"
`

	cli.On("GetLocalStacks").Return(defaultStacks(), nil)
	cli.On("GetLocalStackFile", testStackID).Return(composeWithTraefik, nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleDomainsList()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	var mappings []proxy.DomainMapping
	require.NoError(t, json.Unmarshal([]byte(text), &mappings))

	assert.Len(t, mappings, 1)
	assert.Equal(t, "web", mappings[0].Service)
	assert.Equal(t, "example.com", mappings[0].Domain)
	assert.Equal(t, 80, mappings[0].Port)
	assert.Equal(t, proxy.BackendTraefik, mappings[0].Method)

	cli.AssertExpectations(t)
}

func TestHandleDomainsList_Empty(t *testing.T) {
	cli := new(mockPortainerClient)

	cli.On("GetLocalStacks").Return(defaultStacks(), nil)
	cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)

	h := setupHandlers(t, cli, nil)
	handler := h.HandleDomainsList()
	result, err := handler(context.Background(), newRequest(nil))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	var mappings []proxy.DomainMapping
	require.NoError(t, json.Unmarshal([]byte(text), &mappings))
	assert.Empty(t, mappings)

	cli.AssertExpectations(t)
}

func TestHandleDomainsAdd_Traefik(t *testing.T) {
	cli := new(mockPortainerClient)

	cli.On("GetLocalStacks").Return(defaultStacks(), nil)
	cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)
	cli.On("UpdateLocalStack", testStackID, testEnvID, mock.AnythingOfType("string"), mock.Anything, false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	h.ProxyManager = &proxy.TraefikManager{StackName: testStackName}

	handler := h.HandleDomainsAdd()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "web",
		"domain":  "example.com",
		"port":    float64(80),
		"method":  "traefik",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "example.com")
	assert.Contains(t, text, "web")
	assert.Contains(t, text, "Traefik")
	assert.Contains(t, text, "redeployed")

	cli.AssertExpectations(t)
}

func TestHandleDomainsRemove_Traefik(t *testing.T) {
	cli := new(mockPortainerClient)

	composeWithTraefik := `services:
  web:
    image: nginx:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mystack-web.rule=Host(` + "`" + `example.com` + "`" + `)"
      - "traefik.http.routers.mystack-web.entrypoints=websecure"
      - "traefik.http.routers.mystack-web.tls.certresolver=letsencrypt"
      - "traefik.http.services.mystack-web.loadbalancer.server.port=80"
`

	cli.On("GetLocalStacks").Return(defaultStacks(), nil)
	cli.On("GetLocalStackFile", testStackID).Return(composeWithTraefik, nil)
	cli.On("UpdateLocalStack", testStackID, testEnvID, mock.AnythingOfType("string"), mock.Anything, false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	h.ProxyManager = &proxy.TraefikManager{StackName: testStackName}

	handler := h.HandleDomainsRemove()
	result, err := handler(context.Background(), newRequest(map[string]any{
		"service": "web",
		"domain":  "example.com",
		"method":  "traefik",
	}))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "example.com")
	assert.Contains(t, text, "removed")
	assert.Contains(t, text, "Traefik")

	cli.AssertExpectations(t)
}

// ---------- RecordDeployment integration test ----------

func TestRecordDeployment(t *testing.T) {
	cli := new(mockPortainerClient)

	// readDeployments: first call, empty
	stacks := defaultStacks()
	cli.On("GetLocalStacks").Return(stacks, nil)
	cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)
	cli.On("UpdateLocalStack", testStackID, testEnvID, sampleComposeFile, mock.MatchedBy(func(env []models.LocalStackEnvVar) bool {
		if len(env) != 1 {
			return false
		}
		if env[0].Name != "_TOWLINE_DEPLOYMENTS" {
			return false
		}
		var entries []DeploymentEntry
		if err := json.Unmarshal([]byte(env[0].Value), &entries); err != nil {
			return false
		}
		return len(entries) == 1 && entries[0].Description == "test deploy" && entries[0].Outcome == "success"
	}), false, false).Return(nil)

	h := setupHandlers(t, cli, nil)
	err := h.RecordDeployment("test deploy", sampleComposeFile, sampleComposeFile, "success")
	require.NoError(t, err)

	cli.AssertExpectations(t)
}
