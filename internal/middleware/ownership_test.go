package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractContainerID(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/containers/abc123/json", "abc123"},
		{"/containers/abc123/logs", "abc123"},
		{"/containers/abc123/start", "abc123"},
		{"/containers/abc123/stop", "abc123"},
		{"/containers/abc123/restart", "abc123"},
		{"/containers/abc123/exec", "abc123"},
		{"/containers/json", ""},
		{"/containers/create", ""},
		{"/images/json", ""},
		{"/networks/abc123", ""},
		{"/containers/my-container-name/json", "my-container-name"},
		{"/containers/sha256:abc123/json", "sha256:abc123"},
		{"", ""},
		{"/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := extractContainerID(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContainerOwnership_OwnedContainer(t *testing.T) {
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		inspect := map[string]any{
			"Config": map[string]any{
				"Labels": map[string]any{
					"com.docker.compose.project": "myapp",
				},
			},
		}
		data, _ := json.Marshal(inspect)
		return data, nil
	}

	ownership := NewContainerOwnership("myapp", 1, proxyFn)
	mw := ownership.ForDockerProxy()
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"dockerAPIPath": "/containers/abc123/json",
	}))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestContainerOwnership_UnownedContainer(t *testing.T) {
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		inspect := map[string]any{
			"Config": map[string]any{
				"Labels": map[string]any{
					"com.docker.compose.project": "otherapp",
				},
			},
		}
		data, _ := json.Marshal(inspect)
		return data, nil
	}

	ownership := NewContainerOwnership("myapp", 1, proxyFn)
	mw := ownership.ForDockerProxy()
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"dockerAPIPath": "/containers/abc123/logs",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "does not belong to stack")
}

func TestContainerOwnership_ProxyError(t *testing.T) {
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return nil, fmt.Errorf("connection refused")
	}

	ownership := NewContainerOwnership("myapp", 1, proxyFn)
	mw := ownership.ForDockerProxy()
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"dockerAPIPath": "/containers/abc123/json",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	assert.Contains(t, text, "failed to verify container ownership")
}

func TestContainerOwnership_NonContainerPath(t *testing.T) {
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		t.Fatal("proxy should not be called for non-container paths")
		return nil, nil
	}

	ownership := NewContainerOwnership("myapp", 1, proxyFn)
	mw := ownership.ForDockerProxy()
	handler := mw(passthroughHandler())

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"dockerAPIPath": "/images/json",
	}))
	require.NoError(t, err)
	assert.Equal(t, "executed", result.Content[0].(mcp.TextContent).Text)
}

func TestContainerOwnership_FilterContainerList(t *testing.T) {
	containers := []map[string]any{
		{
			"Id":     "container1",
			"Names":  []any{"/myapp-web-1"},
			"Labels": map[string]any{"com.docker.compose.project": "myapp"},
		},
		{
			"Id":     "container2",
			"Names":  []any{"/otherapp-web-1"},
			"Labels": map[string]any{"com.docker.compose.project": "otherapp"},
		},
		{
			"Id":     "container3",
			"Names":  []any{"/myapp-db-1"},
			"Labels": map[string]any{"com.docker.compose.project": "myapp"},
		},
	}
	data, _ := json.Marshal(containers)

	listDockerHandler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(string(data)), nil
	}

	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		return nil, nil
	}

	ownership := NewContainerOwnership("myapp", 1, proxyFn)
	mw := ownership.ForDockerProxy()
	handler := mw(listDockerHandler)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"dockerAPIPath": "/containers/json",
	}))
	require.NoError(t, err)

	text := result.Content[0].(mcp.TextContent).Text
	var filtered []map[string]any
	err = json.Unmarshal([]byte(text), &filtered)
	require.NoError(t, err)

	assert.Len(t, filtered, 2)
	for _, c := range filtered {
		labels := c["Labels"].(map[string]any)
		assert.Equal(t, "myapp", labels["com.docker.compose.project"])
	}
}

func TestContainerOwnership_BlockedPaths(t *testing.T) {
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		t.Fatal("proxy should not be called for blocked paths")
		return nil, nil
	}

	tests := []struct {
		path   string
		method string
		block  bool
	}{
		{"/networks/create", "POST", true},
		{"/networks/abc123", "GET", false}, // GET is allowed
		{"/volumes/create", "POST", true},
		{"/swarm/init", "POST", true},
		{"/secrets/create", "POST", true},
		{"/exec/abc123/start", "POST", true},
		{"/exec/abc123/json", "GET", false}, // GET is allowed
		{"/images/create", "POST", true},
		{"/containers/json", "GET", false}, // container list is allowed
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.method, tt.path), func(t *testing.T) {
			ownership := NewContainerOwnership("myapp", 1, proxyFn)
			mw := ownership.ForDockerProxy()

			var called bool
			handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				return mcp.NewToolResultText("executed"), nil
			})

			result, err := handler(context.Background(), makeRequest(map[string]any{
				"dockerAPIPath": tt.path,
				"method":        tt.method,
			}))
			require.NoError(t, err)

			if tt.block {
				text := result.Content[0].(mcp.TextContent).Text
				assert.Contains(t, text, "not allowed")
				assert.False(t, called)
			}
		})
	}
}
