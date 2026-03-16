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

// Add parses compose YAML, adds Traefik labels to the specified service, and returns updated compose.
func (m *TraefikManager) Add(composeContent, service, domain string, port int) (string, error) {
	var compose map[string]interface{}
	if err := yaml.Unmarshal([]byte(composeContent), &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose file: %w", err)
	}

	services, ok := compose["services"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("compose file has no services section")
	}

	svcDef, ok := services[service].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("service %q not found in compose file", service)
	}

	router := m.routerName(service)

	labels := map[string]string{
		"traefik.enable": "true",
		fmt.Sprintf("traefik.http.routers.%s.rule", router):                fmt.Sprintf("Host(`%s`)", domain),
		fmt.Sprintf("traefik.http.routers.%s.entrypoints", router):         "websecure",
		fmt.Sprintf("traefik.http.routers.%s.tls.certresolver", router):    "letsencrypt",
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", router): strconv.Itoa(port),
	}

	// Get existing labels or create new ones
	existing := getLabelsMap(svcDef)
	for k, v := range labels {
		existing[k] = v
	}

	// Convert back to a slice of "key=value" strings for compose
	var labelSlice []string
	for k, v := range existing {
		labelSlice = append(labelSlice, fmt.Sprintf("%s=%s", k, v))
	}

	svcDef["labels"] = labelSlice
	services[service] = svcDef
	compose["services"] = services

	out, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(out), nil
}

// Remove removes Traefik labels for the given domain from the specified service.
func (m *TraefikManager) Remove(composeContent, service, domain string) (string, error) {
	var compose map[string]interface{}
	if err := yaml.Unmarshal([]byte(composeContent), &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose file: %w", err)
	}

	services, ok := compose["services"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("compose file has no services section")
	}

	svcDef, ok := services[service].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("service %q not found in compose file", service)
	}

	router := m.routerName(service)
	existing := getLabelsMap(svcDef)

	// Remove all labels associated with this router
	routerPrefix := fmt.Sprintf("traefik.http.routers.%s.", router)
	servicePrefix := fmt.Sprintf("traefik.http.services.%s.", router)

	// Check if the Host rule actually matches the domain we want to remove
	ruleKey := fmt.Sprintf("traefik.http.routers.%s.rule", router)
	if ruleVal, ok := existing[ruleKey]; ok {
		if !strings.Contains(ruleVal, domain) {
			return "", fmt.Errorf("domain %q not found on service %q", domain, service)
		}
	}

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
		delete(svcDef, "labels")
	} else {
		var labelSlice []string
		for k, v := range existing {
			labelSlice = append(labelSlice, fmt.Sprintf("%s=%s", k, v))
		}
		svcDef["labels"] = labelSlice
	}

	services[service] = svcDef
	compose["services"] = services

	out, err := yaml.Marshal(compose)
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

// getLabelsMap extracts labels from a compose service definition into a map.
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
