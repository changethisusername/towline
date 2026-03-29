package towline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/changethisusername/towline/internal/proxy"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// getComposeFile reads the compose file content for the current stack from Portainer.
func (h *Handlers) getComposeFile() (string, int, error) {
	stacks, err := h.Server.Client().GetLocalStacks()
	if err != nil {
		return "", 0, fmt.Errorf("failed to get stacks: %w", err)
	}

	for _, s := range stacks {
		if s.Name == h.StackName {
			compose, err := h.Server.Client().GetLocalStackFile(s.ID)
			if err != nil {
				return "", 0, fmt.Errorf("failed to get stack compose file: %w", err)
			}
			return compose, s.ID, nil
		}
	}

	return "", 0, fmt.Errorf("stack %q not found", h.StackName)
}

// updateComposeFile deploys the updated compose content back to Portainer.
func (h *Handlers) updateComposeFile(stackID int, compose string) error {
	stacks, err := h.Server.Client().GetLocalStacks()
	if err != nil {
		return fmt.Errorf("failed to get stacks: %w", err)
	}

	var envVars []models.LocalStackEnvVar
	var endpointID int
	for _, s := range stacks {
		if s.ID == stackID {
			envVars = s.Env
			endpointID = s.EndpointID
			break
		}
	}

	return h.Server.Client().UpdateLocalStack(stackID, endpointID, compose, envVars, false, false)
}

// getProxyManager returns the appropriate proxy backend based on the method parameter.
// If method is empty, returns the default ProxyManager configured on the handler.
func (h *Handlers) getProxyManager(method string) (proxy.Manager, error) {
	backend := proxy.Backend(method)
	switch backend {
	case proxy.BackendTraefik:
		return &proxy.TraefikManager{StackName: h.StackName}, nil
	case proxy.BackendCaddy:
		if h.CaddyManager != nil {
			return h.CaddyManager, nil
		}
		return nil, fmt.Errorf("Caddy proxy backend not configured")
	case proxy.BackendCloudflare:
		return &proxy.CloudflareManager{StackName: h.StackName}, nil
	case proxy.BackendNone:
		if h.ProxyManager != nil {
			return h.ProxyManager, nil
		}
		return nil, fmt.Errorf("no proxy backend configured; specify 'method' parameter (traefik, caddy, or cloudflare)")
	default:
		return nil, fmt.Errorf("unknown proxy method %q; use traefik, caddy, or cloudflare", method)
	}
}

// HandleDomainsList returns a handler for the towline_domains_list tool.
func (h *Handlers) HandleDomainsList() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		compose, _, err := h.getComposeFile()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get compose file", err), nil
		}

		// Collect mappings from all available backends
		var allMappings []proxy.DomainMapping

		// Always check Traefik labels in compose
		traefik := &proxy.TraefikManager{StackName: h.StackName}
		traefikMappings, err := traefik.List(compose)
		if err == nil {
			allMappings = append(allMappings, traefikMappings...)
		}

		// Check Caddy if configured
		if h.CaddyManager != nil {
			caddyMappings, err := h.CaddyManager.List(compose)
			if err == nil {
				allMappings = append(allMappings, caddyMappings...)
			}
		}

		if allMappings == nil {
			allMappings = []proxy.DomainMapping{}
		}

		jsonData, err := json.Marshal(allMappings)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to marshal domain mappings", err), nil
		}

		return mcp.NewToolResultText(string(jsonData)), nil
	}
}

// HandleDomainsAdd returns a handler for the towline_domains_add tool.
func (h *Handlers) HandleDomainsAdd() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		domain, err := parser.GetString("domain", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid domain parameter", err), nil
		}

		port, err := parser.GetInt("port", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid port parameter", err), nil
		}
		if port <= 0 {
			port = 80
		}

		method, err := parser.GetString("method", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid method parameter", err), nil
		}

		mgr, err := h.getProxyManager(method)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		compose, stackID, err := h.getComposeFile()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get compose file", err), nil
		}

		previousCompose := compose

		result, err := mgr.Add(compose, service, domain, port)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to add domain", err), nil
		}

		// For Traefik, the compose file is modified, so we need to redeploy
		// For Caddy/Cloudflare, result is either the unchanged compose or instructions
		effectiveMethod := proxy.Backend(method)
		if effectiveMethod == proxy.BackendNone || effectiveMethod == "" {
			if h.ProxyManager != nil {
				switch h.ProxyManager.(type) {
				case *proxy.TraefikManager:
					effectiveMethod = proxy.BackendTraefik
				case *proxy.CaddyManager:
					effectiveMethod = proxy.BackendCaddy
				case *proxy.CloudflareManager:
					effectiveMethod = proxy.BackendCloudflare
				}
			}
		}

		if effectiveMethod == proxy.BackendTraefik {
			if err := h.updateComposeFile(stackID, result); err != nil {
				return mcp.NewToolResultErrorFromErr("failed to redeploy compose", err), nil
			}

			// Record deployment
			_ = h.RecordDeployment(
				fmt.Sprintf("Add domain %s -> %s:%d via traefik", domain, service, port),
				previousCompose, result, "success",
			)

			return mcp.NewToolResultText(fmt.Sprintf("Domain %s added to service %s (port %d) via Traefik and redeployed", domain, service, port)), nil
		}

		if effectiveMethod == proxy.BackendCloudflare {
			// Cloudflare returns instructions, not a modified compose
			return mcp.NewToolResultText(result), nil
		}

		// Caddy: route was added via API, no compose change needed
		return mcp.NewToolResultText(fmt.Sprintf("Domain %s added to service %s (port %d) via Caddy", domain, service, port)), nil
	}
}

// HandleDomainsRemove returns a handler for the towline_domains_remove tool.
func (h *Handlers) HandleDomainsRemove() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		domain, err := parser.GetString("domain", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid domain parameter", err), nil
		}

		method, err := parser.GetString("method", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid method parameter", err), nil
		}

		mgr, err := h.getProxyManager(method)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		compose, stackID, err := h.getComposeFile()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get compose file", err), nil
		}

		previousCompose := compose

		result, err := mgr.Remove(compose, service, domain)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to remove domain", err), nil
		}

		// Determine effective method for post-processing
		effectiveMethod := proxy.Backend(method)
		if effectiveMethod == proxy.BackendNone || effectiveMethod == "" {
			if h.ProxyManager != nil {
				switch h.ProxyManager.(type) {
				case *proxy.TraefikManager:
					effectiveMethod = proxy.BackendTraefik
				case *proxy.CaddyManager:
					effectiveMethod = proxy.BackendCaddy
				case *proxy.CloudflareManager:
					effectiveMethod = proxy.BackendCloudflare
				}
			}
		}

		if effectiveMethod == proxy.BackendTraefik {
			if err := h.updateComposeFile(stackID, result); err != nil {
				return mcp.NewToolResultErrorFromErr("failed to redeploy compose", err), nil
			}

			_ = h.RecordDeployment(
				fmt.Sprintf("Remove domain %s from %s via traefik", domain, service),
				previousCompose, result, "success",
			)

			return mcp.NewToolResultText(fmt.Sprintf("Domain %s removed from service %s via Traefik and redeployed", domain, service)), nil
		}

		if effectiveMethod == proxy.BackendCloudflare {
			return mcp.NewToolResultText(result), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Domain %s removed from service %s via Caddy", domain, service)), nil
	}
}
