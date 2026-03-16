package towline

import (
	"context"
	"fmt"
	"strings"

	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

// HandleScale returns a handler for the towline_scale tool.
// It parses compose YAML, sets deploy.replicas on the target service, and redeploys.
func (h *Handlers) HandleScale() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		replicas, err := parser.GetInt("replicas", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid replicas parameter", err), nil
		}

		if replicas < 0 {
			return mcp.NewToolResultError("replicas must be >= 0"), nil
		}

		compose, stackID, err := h.getComposeFile()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get compose file", err), nil
		}

		previousCompose := compose

		updatedCompose, warning, err := setServiceReplicas(compose, service, replicas)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to update replicas", err), nil
		}

		if err := h.updateComposeFile(stackID, updatedCompose); err != nil {
			return mcp.NewToolResultErrorFromErr("failed to redeploy compose", err), nil
		}

		_ = h.RecordDeployment(
			fmt.Sprintf("Scale %s to %d replicas", service, replicas),
			previousCompose, updatedCompose, "success",
		)

		msg := fmt.Sprintf("Service %s scaled to %d replicas", service, replicas)
		if warning != "" {
			msg += "\nWarning: " + warning
		}

		return mcp.NewToolResultText(msg), nil
	}
}

// setServiceReplicas parses compose YAML, sets deploy.replicas for the given service,
// and returns the updated compose content. Also returns a warning if volumes are present.
func setServiceReplicas(composeContent, service string, replicas int) (string, string, error) {
	var compose map[string]interface{}
	if err := yaml.Unmarshal([]byte(composeContent), &compose); err != nil {
		return "", "", fmt.Errorf("failed to parse compose file: %w", err)
	}

	services, ok := compose["services"].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("compose file has no services section")
	}

	svcDef, ok := services[service].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("service %q not found in compose file", service)
	}

	// Check for volumes that might cause issues with multiple replicas
	var warning string
	if volumes, ok := svcDef["volumes"]; ok && replicas > 1 {
		if volumeList, ok := volumes.([]interface{}); ok && len(volumeList) > 0 {
			var namedVolumes []string
			for _, v := range volumeList {
				vStr, ok := v.(string)
				if !ok {
					continue
				}
				// Named volumes (not host-bind mounts) start without / or ./
				if !strings.HasPrefix(vStr, "/") && !strings.HasPrefix(vStr, "./") && !strings.HasPrefix(vStr, "../") {
					namedVolumes = append(namedVolumes, vStr)
				}
			}
			if len(namedVolumes) > 0 {
				warning = fmt.Sprintf("service has volumes that may not work correctly with multiple replicas: %s", strings.Join(namedVolumes, ", "))
			}
		}
	}

	// Set deploy.replicas
	deploy, ok := svcDef["deploy"].(map[string]interface{})
	if !ok {
		deploy = make(map[string]interface{})
	}
	deploy["replicas"] = replicas
	svcDef["deploy"] = deploy

	services[service] = svcDef
	compose["services"] = services

	out, err := yaml.Marshal(compose)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(out), warning, nil
}
