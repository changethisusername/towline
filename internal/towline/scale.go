package towline

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

// maxReplicas caps towline_scale so a single call cannot start an
// unbounded number of containers on the shared host.
const maxReplicas = 20

// validateReplicas checks that n is a whole number in [0, maxReplicas].
// Fractions are rejected rather than truncated: 0.5 must not become 0
// and stop the service.
func validateReplicas(n float64) (int, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
		return 0, fmt.Errorf("replicas must be a whole number, got %v", n)
	}
	if n < 0 {
		return 0, fmt.Errorf("replicas must be >= 0, got %v", n)
	}
	if n > maxReplicas {
		return 0, fmt.Errorf("replicas must be <= %d, got %v", maxReplicas, n)
	}
	return int(n), nil
}

// HandleScale returns a handler for the towline_scale tool.
// It parses compose YAML, sets deploy.replicas on the target service, and redeploys.
func (h *Handlers) HandleScale() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		replicasNum, err := parser.GetNumber("replicas", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid replicas parameter", err), nil
		}

		replicas, err := validateReplicas(replicasNum)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		h.stackMu.Lock()
		defer h.stackMu.Unlock()

		compose, stackID, err := h.getComposeFile()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get compose file", err), nil
		}

		previousCompose := compose

		updatedCompose, warning, err := setServiceReplicas(compose, service, replicas)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to update replicas", err), nil
		}

		desc := fmt.Sprintf("Scale %s to %d replicas", service, replicas)
		if err := h.updateComposeFile(stackID, previousCompose, updatedCompose, desc); err != nil {
			return mcp.NewToolResultErrorFromErr("failed to redeploy compose", err), nil
		}

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
	if err := checkScaleNotShared(root, svcNode, deployNode, service); err != nil {
		return "", "", err
	}
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

// checkScaleNotShared refuses to scale a service whose deploy settings are
// shared with, or inherited from, other YAML nodes. Editing them in place
// would scale every service that reuses them, and adding a deploy key next
// to a merge key would replace (not extend) the inherited deploy block and
// drop its limits. The agent is asked to give the service its own deploy
// block instead of having the compose file silently rewritten.
func checkScaleNotShared(root, svcNode, deployNode *yaml.Node, service string) error {
	if svcNode.Kind == yaml.AliasNode {
		return fmt.Errorf("service %q is a YAML alias (*%s) of another definition; scaling it would also scale the original. Give the service its own definition, then scale", service, svcNode.Value)
	}
	if svcNode.Kind != yaml.MappingNode {
		return fmt.Errorf("service %q is not a mapping", service)
	}
	if svcNode.Anchor != "" && yamlIsAliased(root, svcNode) {
		return fmt.Errorf("service %q is anchored (&%s) and reused by other services; changing its deploy settings would change them too. Give the service its own deploy block outside the anchor, then scale", service, svcNode.Anchor)
	}

	if deployNode == nil {
		if yamlMergeHasKey(svcNode, "deploy") {
			return fmt.Errorf("service %q inherits deploy settings through a YAML merge key (<<); adding replicas would replace the inherited deploy block and drop its settings. Give the service its own deploy block, then scale", service)
		}
		return nil
	}

	switch {
	case deployNode.Kind == yaml.AliasNode:
		return fmt.Errorf("deploy of service %q is a YAML alias (*%s) shared with other services; scaling would change all of them. Give the service its own deploy block, then scale", service, deployNode.Value)
	case deployNode.Kind != yaml.MappingNode:
		return fmt.Errorf("deploy of service %q is not a mapping", service)
	case deployNode.Anchor != "" && yamlIsAliased(root, deployNode):
		return fmt.Errorf("deploy of service %q is anchored (&%s) and reused by other services; scaling would change all of them. Give the service its own deploy block, then scale", service, deployNode.Anchor)
	}

	if r := yamlMapLookup(deployNode, "replicas"); r != nil {
		if r.Kind == yaml.AliasNode || (r.Anchor != "" && yamlIsAliased(root, r)) {
			return fmt.Errorf("deploy.replicas of service %q is a shared YAML anchor or alias; replace it with a plain number, then scale", service)
		}
	}
	return nil
}

// yamlIsAliased reports whether any alias node under n refers to target.
func yamlIsAliased(n, target *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode && n.Alias == target {
		return true
	}
	for _, c := range n.Content {
		if yamlIsAliased(c, target) {
			return true
		}
	}
	return false
}

// yamlMergeHasKey reports whether mapping inherits key through a merge key
// (<<), directly or through nested merges.
func yamlMergeHasKey(mapping *yaml.Node, key string) bool {
	return yamlMergeHasKeyDepth(mapping, key, 0)
}

func yamlMergeHasKeyDepth(mapping *yaml.Node, key string, depth int) bool {
	if mapping == nil || depth > 16 {
		return false
	}
	if mapping.Kind == yaml.AliasNode {
		mapping = mapping.Alias
	}
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Value != "<<" && k.Tag != "!!merge" {
			continue
		}
		v := mapping.Content[i+1]
		sources := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			sources = v.Content
		}
		for _, src := range sources {
			if src.Kind == yaml.AliasNode {
				src = src.Alias
			}
			if src == nil {
				continue
			}
			if yamlMapLookup(src, key) != nil || yamlMergeHasKeyDepth(src, key, depth+1) {
				return true
			}
		}
	}
	return false
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
