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

// setServiceReplicas uses yaml.Node to set deploy.replicas on a compose service
// while preserving comments, key ordering, and anchors.
func setServiceReplicas(composeContent, service string, replicas int) (string, string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(composeContent), &doc); err != nil {
		return "", "", fmt.Errorf("failed to parse compose file: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", "", fmt.Errorf("invalid compose document")
	}
	root := doc.Content[0]

	servicesNode := yamlMapLookup(root, "services")
	if servicesNode == nil {
		return "", "", fmt.Errorf("compose file has no services section")
	}

	svcNode := yamlMapLookup(servicesNode, service)
	if svcNode == nil {
		return "", "", fmt.Errorf("service %q not found in compose file", service)
	}

	// Check for volumes warning
	var warning string
	if replicas > 1 {
		warning = checkVolumeWarning(svcNode)
	}

	// Navigate to or create deploy.replicas
	deployNode := yamlMapLookup(svcNode, "deploy")
	if deployNode == nil {
		// Create the deploy mapping and append to service
		deployKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "deploy", Tag: "!!str"}
		deployNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		svcNode.Content = append(svcNode.Content, deployKey, deployNode)
	}

	yamlMapSet(deployNode, "replicas", fmt.Sprintf("%d", replicas), "!!int")

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(out), warning, nil
}

// yamlMapLookup finds a key in a yaml.Node mapping and returns the value node.
func yamlMapLookup(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// yamlMapSet sets a key-value pair in a yaml.Node mapping. Creates the key if it doesn't exist.
func yamlMapSet(mapping *yaml.Node, key, value, tag string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = tag
			mapping.Content[i+1].Kind = yaml.ScalarNode
			return
		}
	}
	// Key not found — append
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: tag}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}

// checkVolumeWarning checks for named volumes that may conflict with multiple replicas.
func checkVolumeWarning(svcNode *yaml.Node) string {
	volumesNode := yamlMapLookup(svcNode, "volumes")
	if volumesNode == nil || volumesNode.Kind != yaml.SequenceNode {
		return ""
	}

	var namedVolumes []string
	for _, item := range volumesNode.Content {
		if item.Kind != yaml.ScalarNode {
			continue
		}
		v := item.Value
		if !strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "./") && !strings.HasPrefix(v, "../") {
			namedVolumes = append(namedVolumes, v)
		}
	}
	if len(namedVolumes) > 0 {
		return fmt.Sprintf("service has volumes that may not work correctly with multiple replicas: %s", strings.Join(namedVolumes, ", "))
	}
	return ""
}
