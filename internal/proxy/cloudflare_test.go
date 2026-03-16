package proxy

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudflareManager_Add(t *testing.T) {
	m := &CloudflareManager{StackName: "mystack"}

	result, err := m.Add(testCompose, "web", "example.com", 80)
	require.NoError(t, err)

	// Parse JSON output
	var instruction cloudflareInstruction
	err = json.Unmarshal([]byte(result), &instruction)
	require.NoError(t, err)

	assert.Equal(t, "requires_cloudflare_mcp", instruction.Status)
	require.Len(t, instruction.Steps, 4)

	// Verify steps mention tunnel_create and tunnel_route
	assert.Contains(t, instruction.Steps[0], "cloudflare_tunnel_create")
	assert.Contains(t, instruction.Steps[1], "cloudflare_tunnel_route")
}

func TestCloudflareManager_Remove(t *testing.T) {
	m := &CloudflareManager{StackName: "mystack"}

	result, err := m.Remove(testCompose, "web", "example.com")
	require.NoError(t, err)

	// Should be valid JSON
	var instruction cloudflareInstruction
	err = json.Unmarshal([]byte(result), &instruction)
	require.NoError(t, err)

	// Should mention Cloudflare MCP
	assert.Equal(t, "requires_cloudflare_mcp", instruction.Status)
	assert.Contains(t, result, "cloudflare_tunnel_route")
	assert.Contains(t, result, "cloudflare_tunnel_delete")
}

func TestCloudflareManager_List(t *testing.T) {
	m := &CloudflareManager{StackName: "mystack"}

	mappings, err := m.List(testCompose)
	assert.NoError(t, err)
	assert.Nil(t, mappings, "List should return nil since tunnels are managed externally")
}

func TestCloudflareManager_Add_VerifySteps(t *testing.T) {
	tests := []struct {
		name      string
		stackName string
		service   string
		domain    string
		port      int
	}{
		{
			name:      "web service",
			stackName: "myapp",
			service:   "web",
			domain:    "web.example.com",
			port:      80,
		},
		{
			name:      "api service with custom port",
			stackName: "production",
			service:   "api",
			domain:    "api.mysite.io",
			port:      8443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &CloudflareManager{StackName: tt.stackName}

			result, err := m.Add("services: {}", tt.service, tt.domain, tt.port)
			require.NoError(t, err)

			var instruction cloudflareInstruction
			err = json.Unmarshal([]byte(result), &instruction)
			require.NoError(t, err)

			assert.Equal(t, "requires_cloudflare_mcp", instruction.Status)
			require.Len(t, instruction.Steps, 4)

			// Step 1: tunnel_create with stack-service name
			assert.Contains(t, instruction.Steps[0], tt.stackName+"-"+tt.service)
			assert.Contains(t, instruction.Steps[0], "cloudflare_tunnel_create")

			// Step 2: tunnel_route with domain and service:port
			assert.Contains(t, instruction.Steps[1], tt.domain)
			assert.Contains(t, instruction.Steps[1], tt.service)
			assert.Contains(t, instruction.Steps[1], "cloudflare_tunnel_route")

			// Step 3: tunnel token for the service
			assert.Contains(t, instruction.Steps[2], tt.service)
			assert.Contains(t, instruction.Steps[2], "CLOUDFLARE_TUNNEL_TOKEN")

			// Step 4: cloudflared sidecar
			assert.Contains(t, instruction.Steps[3], "cloudflared")
		})
	}
}

func TestCloudflareManager_Remove_VerifySteps(t *testing.T) {
	m := &CloudflareManager{StackName: "mystack"}

	result, err := m.Remove("services: {}", "api", "api.example.com")
	require.NoError(t, err)

	var instruction cloudflareInstruction
	err = json.Unmarshal([]byte(result), &instruction)
	require.NoError(t, err)

	require.Len(t, instruction.Steps, 3)

	// Step 1: remove route
	assert.Contains(t, instruction.Steps[0], "api.example.com")
	assert.Contains(t, instruction.Steps[0], "cloudflare_tunnel_route")

	// Step 2: delete tunnel
	assert.Contains(t, instruction.Steps[1], "mystack-api")
	assert.Contains(t, instruction.Steps[1], "cloudflare_tunnel_delete")

	// Step 3: remove sidecar
	assert.Contains(t, instruction.Steps[2], "cloudflared")
}
