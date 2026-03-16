package proxy

import (
	"encoding/json"
	"fmt"
)

// CloudflareManager returns structured instructions for the agent to use Cloudflare MCP.
// It does not call Cloudflare APIs directly.
type CloudflareManager struct {
	StackName string
}

// cloudflareInstruction is the structured response telling the agent what to do.
type cloudflareInstruction struct {
	Status string   `json:"status"`
	Steps  []string `json:"steps"`
}

// Add returns JSON instructions for the agent to create a Cloudflare tunnel via MCP.
func (m *CloudflareManager) Add(composeContent, service, domain string, port int) (string, error) {
	instruction := cloudflareInstruction{
		Status: "requires_cloudflare_mcp",
		Steps: []string{
			fmt.Sprintf("Use cloudflare_tunnel_create to create a tunnel named '%s-%s'", m.StackName, service),
			fmt.Sprintf("Use cloudflare_tunnel_route to route '%s' to 'http://%s:%d'", domain, service, port),
			fmt.Sprintf("Add the tunnel token as CLOUDFLARE_TUNNEL_TOKEN to the '%s' service environment", service),
			"Ensure the cloudflared sidecar container is added to the compose file",
		},
	}

	jsonData, err := json.MarshalIndent(instruction, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal instructions: %w", err)
	}

	return string(jsonData), nil
}

// Remove returns text instructions for the agent to remove a Cloudflare tunnel via MCP.
func (m *CloudflareManager) Remove(composeContent, service, domain string) (string, error) {
	instruction := cloudflareInstruction{
		Status: "requires_cloudflare_mcp",
		Steps: []string{
			fmt.Sprintf("Use cloudflare_tunnel_route to remove the route for '%s'", domain),
			fmt.Sprintf("Use cloudflare_tunnel_delete to remove the tunnel '%s-%s' if no other routes exist", m.StackName, service),
			"Remove the cloudflared sidecar container from the compose file if no tunnels remain",
		},
	}

	jsonData, err := json.MarshalIndent(instruction, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal instructions: %w", err)
	}

	return string(jsonData), nil
}

// List returns nil since Cloudflare tunnels are managed externally.
func (m *CloudflareManager) List(composeContent string) ([]DomainMapping, error) {
	return nil, nil
}
