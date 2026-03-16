package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/internal/tooldef"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// defaultComposeFile is a simple compose YAML for testing.
const defaultComposeFile = `version: "3"
services:
  web:
    image: nginx:latest
    ports:
      - "80:80"
  redis:
    image: redis:alpine
`

// mockPortainerClient implements mcp.PortainerClient for integration testing.
type mockPortainerClient struct {
	mu sync.Mutex

	// Configurable state
	stacks    []models.LocalStack
	stackFile string

	// Call tracking
	updateLocalStackCalled bool
	createLocalStackCalled bool
	startLocalStackCalled  bool
	stopLocalStackCalled   bool
	deleteLocalStackCalled bool
	lastUpdateFile         string
	lastUpdateEnv          []models.LocalStackEnvVar
}

func newMockClient() *mockPortainerClient {
	return &mockPortainerClient{
		stacks: []models.LocalStack{
			{
				ID:         1,
				Name:       "testapp-dev",
				Type:       "compose",
				Status:     "active",
				EndpointID: 1,
				Env: []models.LocalStackEnvVar{
					{Name: "APP_PORT", Value: "8080"},
					{Name: "NODE_ENV", Value: "development"},
				},
			},
			{
				ID:         2,
				Name:       "other-app",
				Type:       "compose",
				Status:     "active",
				EndpointID: 1,
			},
		},
		stackFile: defaultComposeFile,
	}
}

// Tag methods
func (m *mockPortainerClient) GetEnvironmentTags() ([]models.EnvironmentTag, error) {
	return nil, nil
}
func (m *mockPortainerClient) CreateEnvironmentTag(name string) (int, error) { return 0, nil }

// Environment methods
func (m *mockPortainerClient) GetEnvironments() ([]models.Environment, error) { return nil, nil }
func (m *mockPortainerClient) UpdateEnvironmentTags(id int, tagIds []int) error {
	return nil
}
func (m *mockPortainerClient) UpdateEnvironmentUserAccesses(id int, userAccesses map[int]string) error {
	return nil
}
func (m *mockPortainerClient) UpdateEnvironmentTeamAccesses(id int, teamAccesses map[int]string) error {
	return nil
}

// Environment Group methods
func (m *mockPortainerClient) GetEnvironmentGroups() ([]models.Group, error) { return nil, nil }
func (m *mockPortainerClient) CreateEnvironmentGroup(name string, environmentIds []int) (int, error) {
	return 0, nil
}
func (m *mockPortainerClient) UpdateEnvironmentGroupName(id int, name string) error { return nil }
func (m *mockPortainerClient) UpdateEnvironmentGroupEnvironments(id int, environmentIds []int) error {
	return nil
}
func (m *mockPortainerClient) UpdateEnvironmentGroupTags(id int, tagIds []int) error { return nil }

// Access Group methods
func (m *mockPortainerClient) GetAccessGroups() ([]models.AccessGroup, error) { return nil, nil }
func (m *mockPortainerClient) CreateAccessGroup(name string, environmentIds []int) (int, error) {
	return 0, nil
}
func (m *mockPortainerClient) UpdateAccessGroupName(id int, name string) error { return nil }
func (m *mockPortainerClient) UpdateAccessGroupUserAccesses(id int, userAccesses map[int]string) error {
	return nil
}
func (m *mockPortainerClient) UpdateAccessGroupTeamAccesses(id int, teamAccesses map[int]string) error {
	return nil
}
func (m *mockPortainerClient) AddEnvironmentToAccessGroup(id int, environmentId int) error {
	return nil
}
func (m *mockPortainerClient) RemoveEnvironmentFromAccessGroup(id int, environmentId int) error {
	return nil
}

// Edge Stack methods
func (m *mockPortainerClient) GetStacks() ([]models.Stack, error) { return nil, nil }
func (m *mockPortainerClient) GetStackFile(id int) (string, error) {
	return "", nil
}
func (m *mockPortainerClient) CreateStack(name string, file string, environmentGroupIds []int) (int, error) {
	return 0, nil
}
func (m *mockPortainerClient) UpdateStack(id int, file string, environmentGroupIds []int) error {
	return nil
}

// Local Stack methods
func (m *mockPortainerClient) GetLocalStacks() ([]models.LocalStack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return a copy to prevent mutation
	result := make([]models.LocalStack, len(m.stacks))
	copy(result, m.stacks)
	return result, nil
}

func (m *mockPortainerClient) GetLocalStackFile(id int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stackFile, nil
}

func (m *mockPortainerClient) CreateLocalStack(endpointId int, name, file string, env []models.LocalStackEnvVar) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createLocalStackCalled = true
	// Return a fixed ID for testing
	return 42, nil
}

func (m *mockPortainerClient) UpdateLocalStack(id, endpointId int, file string, env []models.LocalStackEnvVar, prune, pullImage bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateLocalStackCalled = true
	m.lastUpdateFile = file
	m.lastUpdateEnv = env

	// Update the in-memory stacks for subsequent reads
	for i := range m.stacks {
		if m.stacks[i].ID == id {
			m.stacks[i].Env = env
			break
		}
	}

	return nil
}

func (m *mockPortainerClient) StartLocalStack(id, endpointId int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startLocalStackCalled = true
	return nil
}

func (m *mockPortainerClient) StopLocalStack(id, endpointId int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocalStackCalled = true
	return nil
}

func (m *mockPortainerClient) DeleteLocalStack(id, endpointId int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteLocalStackCalled = true
	return nil
}

// Team methods
func (m *mockPortainerClient) CreateTeam(name string) (int, error)           { return 0, nil }
func (m *mockPortainerClient) GetTeams() ([]models.Team, error)              { return nil, nil }
func (m *mockPortainerClient) UpdateTeamName(id int, name string) error      { return nil }
func (m *mockPortainerClient) UpdateTeamMembers(id int, userIds []int) error { return nil }

// User methods
func (m *mockPortainerClient) GetUsers() ([]models.User, error)            { return nil, nil }
func (m *mockPortainerClient) UpdateUserRole(id int, role string) error     { return nil }

// Settings methods
func (m *mockPortainerClient) GetSettings() (models.PortainerSettings, error) {
	return models.PortainerSettings{}, nil
}

// Version methods
func (m *mockPortainerClient) GetVersion() (string, error) { return "2.31.2", nil }

// Docker Proxy methods
func (m *mockPortainerClient) ProxyDockerRequest(opts models.DockerProxyRequestOptions) (*http.Response, error) {
	body, err := m.proxyDockerRequestRaw(opts)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// proxyDockerRequestRaw returns raw bytes for Docker proxy requests.
// This is used by both ProxyDockerRequest and the ownership proxyFn.
func (m *mockPortainerClient) proxyDockerRequestRaw(opts models.DockerProxyRequestOptions) ([]byte, error) {
	path := opts.Path

	// Container list
	if strings.HasPrefix(path, "/containers/json") {
		containers := []map[string]any{
			{
				"Id":     "owned-container-abc123",
				"Names":  []string{"/testapp-dev-web-1"},
				"State":  "running",
				"Status": "Up 2 hours",
				"Labels": map[string]any{
					"com.docker.compose.project": "testapp-dev",
					"com.docker.compose.service": "web",
				},
			},
			{
				"Id":     "owned-container-def456",
				"Names":  []string{"/testapp-dev-redis-1"},
				"State":  "running",
				"Status": "Up 2 hours",
				"Labels": map[string]any{
					"com.docker.compose.project": "testapp-dev",
					"com.docker.compose.service": "redis",
				},
			},
			{
				"Id":     "foreign-container-id",
				"Names":  []string{"/other-app-web-1"},
				"State":  "running",
				"Status": "Up 1 hour",
				"Labels": map[string]any{
					"com.docker.compose.project": "other-app",
					"com.docker.compose.service": "web",
				},
			},
		}
		data, _ := json.Marshal(containers)
		return data, nil
	}

	// Container inspect (for ownership checks)
	if strings.Contains(path, "/json") && strings.HasPrefix(path, "/containers/") {
		containerID := extractContainerIDFromPath(path)
		project := "other-app"
		if strings.HasPrefix(containerID, "owned-container") {
			project = "testapp-dev"
		}

		inspect := map[string]any{
			"Id": containerID,
			"Config": map[string]any{
				"Labels": map[string]any{
					"com.docker.compose.project": project,
					"com.docker.compose.service": "web",
				},
			},
			"State": map[string]any{
				"Running":    true,
				"StartedAt":  "2026-03-17T08:00:00.000000000Z",
				"FinishedAt": "0001-01-01T00:00:00Z",
				"ExitCode":   0,
			},
			"RestartCount": 0,
		}
		data, _ := json.Marshal(inspect)
		return data, nil
	}

	// Container stats
	if strings.Contains(path, "/stats") {
		stats := map[string]any{
			"cpu_stats": map[string]any{
				"cpu_usage":        map[string]any{"total_usage": 2000000000.0},
				"system_cpu_usage": 20000000000.0,
				"online_cpus":     4.0,
			},
			"precpu_stats": map[string]any{
				"cpu_usage":        map[string]any{"total_usage": 1000000000.0},
				"system_cpu_usage": 10000000000.0,
			},
			"memory_stats": map[string]any{
				"usage": 104857600.0,
				"limit": 2147483648.0,
			},
		}
		data, _ := json.Marshal(stats)
		return data, nil
	}

	// Container logs
	if strings.Contains(path, "/logs") {
		return []byte("2026-03-17T10:00:00Z INFO  Starting server on port 8080\n"), nil
	}

	// Default: empty response
	return []byte("{}"), nil
}

// Kubernetes Proxy methods
func (m *mockPortainerClient) ProxyKubernetesRequest(opts models.KubernetesProxyRequestOptions) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
		Header:     make(http.Header),
	}, nil
}

// extractContainerIDFromPath extracts the container ID from a Docker API path
// like /containers/<id>/json
func extractContainerIDFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "containers" {
		return parts[1]
	}
	return ""
}

// writeTempToolsYAML writes the embedded tools.yaml to a temp file and returns its path.
func writeTempToolsYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tools.yaml")
	err := os.WriteFile(path, tooldef.ToolsFile, 0644)
	if err != nil {
		t.Fatalf("failed to write tools.yaml: %v", err)
	}
	return path
}

// writeTempTowlineToolsYAML writes the embedded towline-tools.yaml to a temp file.
func writeTempTowlineToolsYAML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "towline-tools.yaml")
	err := os.WriteFile(path, tooldef.TowlineToolsFile, 0644)
	if err != nil {
		t.Fatalf("failed to write towline-tools.yaml: %v", err)
	}
	return path
}

// loadTowlineTools loads towline tool definitions from a YAML file.
func loadTowlineTools(path string) (map[string]mcpgo.Tool, error) {
	return toolgen.LoadToolsFromYAML(path, "v1.0.0")
}

// mustMarshalJSON marshals v to JSON or panics.
func mustMarshalJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return string(data)
}
