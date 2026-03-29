package proxy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TraefikManager manages domain routing via Traefik Docker labels in compose files.
type TraefikManager struct {
	StackName string
}

// routerName generates a consistent Traefik router name from stack and service.
func (m *TraefikManager) routerName(service string) string {
	return fmt.Sprintf("%s-%s", m.StackName, service)
}

// domainRegex validates that a domain is a valid hostname/FQDN.
var domainRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

// serviceNameRegex validates compose service names.
var serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-]*$`)

// Add parses compose YAML, adds Traefik labels to the specified service, and returns updated compose.
// Uses yaml.Node to preserve document structure, comments, and key ordering.
func (m *TraefikManager) Add(composeContent, service, domain string, port int) (string, error) {
	if !domainRegex.MatchString(domain) {
		return "", fmt.Errorf("invalid domain %q: must be a valid hostname", domain)
	}
	if !serviceNameRegex.MatchString(service) {
		return "", fmt.Errorf("invalid service name %q: must contain only letters, digits, hyphens, and underscores", service)
	}

	doc, svcNode, err := findServiceNode(composeContent, service)
	if err != nil {
		return "", err
	}

	router := m.routerName(service)

	newLabels := map[string]string{
		"traefik.enable": "true",
		fmt.Sprintf("traefik.http.routers.%s.rule", router):                      fmt.Sprintf("Host(`%s`)", domain),
		fmt.Sprintf("traefik.http.routers.%s.entrypoints", router):               "websecure",
		fmt.Sprintf("traefik.http.routers.%s.tls.certresolver", router):          "letsencrypt",
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", router): strconv.Itoa(port),
	}

	existing := getLabelsMapFromNode(svcNode)
	for k, v := range newLabels {
		existing[k] = v
	}

	setLabelsOnNode(svcNode, existing)

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(out), nil
}

// Remove removes Traefik labels for the given domain from the specified service.
// Uses yaml.Node to preserve document structure, comments, and key ordering.
func (m *TraefikManager) Remove(composeContent, service, domain string) (string, error) {
	doc, svcNode, err := findServiceNode(composeContent, service)
	if err != nil {
		return "", err
	}

	router := m.routerName(service)
	existing := getLabelsMapFromNode(svcNode)

	// Check if the Host rule actually matches the domain we want to remove
	ruleKey := fmt.Sprintf("traefik.http.routers.%s.rule", router)
	if ruleVal, ok := existing[ruleKey]; ok {
		if !strings.Contains(ruleVal, domain) {
			return "", fmt.Errorf("domain %q not found on service %q", domain, service)
		}
	}

	// Remove all labels associated with this router
	routerPrefix := fmt.Sprintf("traefik.http.routers.%s.", router)
	servicePrefix := fmt.Sprintf("traefik.http.services.%s.", router)
	for k := range existing {
		if strings.HasPrefix(k, routerPrefix) || strings.HasPrefix(k, servicePrefix) {
			delete(existing, k)
		}
	}

	// If no traefik labels remain, remove traefik.enable too
	hasTraefikLabels := false
	for k := range existing {
		if strings.HasPrefix(k, "traefik.http.") {
			hasTraefikLabels = true
			break
		}
	}
	if !hasTraefikLabels {
		delete(existing, "traefik.enable")
	}

	if len(existing) == 0 {
		removeNodeKey(svcNode, "labels")
	} else {
		setLabelsOnNode(svcNode, existing)
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(out), nil
}

// hostRegex matches Host(`...`) in Traefik label values.
var hostRegex = regexp.MustCompile(`Host\(` + "`" + `([^` + "`" + `]+)` + "`" + `\)`)

// List scans compose content for Traefik Host() rules and returns domain mappings.
func (m *TraefikManager) List(composeContent string) ([]DomainMapping, error) {
	var compose map[string]interface{}
	if err := yaml.Unmarshal([]byte(composeContent), &compose); err != nil {
		return nil, fmt.Errorf("failed to parse compose file: %w", err)
	}

	services, ok := compose["services"].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	var mappings []DomainMapping

	for svcName, svcRaw := range services {
		svcDef, ok := svcRaw.(map[string]interface{})
		if !ok {
			continue
		}

		labels := getLabelsMap(svcDef)

		// Find Host() rules and port settings
		for key, value := range labels {
			matches := hostRegex.FindStringSubmatch(value)
			if len(matches) < 2 {
				continue
			}

			domain := matches[1]

			// Extract the router name from the label key to find the corresponding port
			// e.g., traefik.http.routers.mystack-web.rule -> mystack-web
			routerName := extractRouterName(key)
			port := 0
			if routerName != "" {
				portKey := fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerName)
				if portStr, ok := labels[portKey]; ok {
					if p, err := strconv.Atoi(portStr); err == nil {
						port = p
					}
				}
			}

			mappings = append(mappings, DomainMapping{
				Service: svcName,
				Domain:  domain,
				Port:    port,
				Method:  BackendTraefik,
			})
		}
	}

	return mappings, nil
}

// extractRouterName extracts the router name from a traefik label key like
// "traefik.http.routers.mystack-web.rule" -> "mystack-web"
func extractRouterName(key string) string {
	const prefix = "traefik.http.routers."
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	rest := key[len(prefix):]
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// findServiceNode parses compose YAML into a yaml.Node document and locates
// the node for the given service name.
func findServiceNode(composeContent, service string) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(composeContent), &doc); err != nil {
		return nil, nil, fmt.Errorf("failed to parse compose file: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil, fmt.Errorf("invalid compose document")
	}
	root := doc.Content[0]

	servicesNode := nodeLookup(root, "services")
	if servicesNode == nil {
		return nil, nil, fmt.Errorf("compose file has no services section")
	}

	svcNode := nodeLookup(servicesNode, service)
	if svcNode == nil {
		return nil, nil, fmt.Errorf("service %q not found in compose file", service)
	}

	return &doc, svcNode, nil
}

// nodeLookup finds a key in a yaml.Node mapping and returns the value node.
func nodeLookup(mapping *yaml.Node, key string) *yaml.Node {
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

// getLabelsMapFromNode reads labels from a service yaml.Node (handles both map and sequence).
func getLabelsMapFromNode(svcNode *yaml.Node) map[string]string {
	result := make(map[string]string)
	labelsNode := nodeLookup(svcNode, "labels")
	if labelsNode == nil {
		return result
	}

	switch labelsNode.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(labelsNode.Content); i += 2 {
			result[labelsNode.Content[i].Value] = labelsNode.Content[i+1].Value
		}
	case yaml.SequenceNode:
		for _, item := range labelsNode.Content {
			if item.Kind == yaml.ScalarNode {
				parts := strings.SplitN(item.Value, "=", 2)
				if len(parts) == 2 {
					result[parts[0]] = parts[1]
				}
			}
		}
	}

	return result
}

// setLabelsOnNode replaces the labels key on a service node with a sequence of "key=value" strings.
func setLabelsOnNode(svcNode *yaml.Node, labels map[string]string) {
	// Build a new sequence node for labels
	labelsNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for k, v := range labels {
		labelsNode.Content = append(labelsNode.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: fmt.Sprintf("%s=%s", k, v),
			Tag:   "!!str",
		})
	}

	// Find and replace existing labels node, or append
	for i := 0; i+1 < len(svcNode.Content); i += 2 {
		if svcNode.Content[i].Value == "labels" {
			svcNode.Content[i+1] = labelsNode
			return
		}
	}
	// Not found — append
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: "labels", Tag: "!!str"}
	svcNode.Content = append(svcNode.Content, keyNode, labelsNode)
}

// removeNodeKey removes a key-value pair from a mapping node.
func removeNodeKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// getLabelsMap extracts labels from a compose service definition into a map.
// Used by List which operates on the unmarshalled map representation.
// Handles both map[string]interface{} and []interface{} (key=value) formats.
func getLabelsMap(svcDef map[string]interface{}) map[string]string {
	result := make(map[string]string)

	labelsRaw, ok := svcDef["labels"]
	if !ok {
		return result
	}

	switch labels := labelsRaw.(type) {
	case map[string]interface{}:
		for k, v := range labels {
			result[k] = fmt.Sprintf("%v", v)
		}
	case []interface{}:
		for _, item := range labels {
			str, ok := item.(string)
			if !ok {
				continue
			}
			parts := strings.SplitN(str, "=", 2)
			if len(parts) == 2 {
				result[parts[0]] = parts[1]
			}
		}
	}

	return result
}
