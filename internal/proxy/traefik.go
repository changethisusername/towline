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

// singleHostRule returns the host of a rule of the exact form Host(`host`).
func singleHostRule(rule string) (string, bool) {
	m := singleHostRuleRegex.FindStringSubmatch(strings.TrimSpace(rule))
	if m == nil {
		return "", false
	}
	return m[1], true
}

var singleHostRuleRegex = regexp.MustCompile("^Host\\(`([^`]+)`\\)$")

// Add parses compose YAML, adds Traefik labels to the specified service, and returns updated compose.
//
// Labels are edited in place: existing labels keep their order and form,
// traefik.* entries this router needs are updated where they are and the
// others appended. A service has one Towline router, so a second domain on
// the same service is refused rather than silently replacing the first.
// Labels shared through YAML anchors, aliases or merge keys are refused
// rather than rewritten.
func (m *TraefikManager) Add(composeContent, service, domain string, port int) (string, error) {
	if err := validateRoute(service, domain); err != nil {
		return "", err
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid port %d", port)
	}

	doc, svcNode, err := findServiceNode(composeContent, service)
	if err != nil {
		return "", err
	}
	labels, err := editableLabels(doc.Content[0], svcNode, service)
	if err != nil {
		return "", err
	}

	router := m.routerName(service)
	ruleKey := fmt.Sprintf("traefik.http.routers.%s.rule", router)
	if rule, ok := labels.get(ruleKey); ok {
		if host, single := singleHostRule(rule); single && strings.EqualFold(host, domain) {
			return "", fmt.Errorf("domain %q is already routed to service %q", domain, service)
		}
		return "", fmt.Errorf("service %q already has a Traefik route (%s=%s); only one domain per service is supported, remove the existing domain first", service, ruleKey, rule)
	}

	// The same host on another service of this stack would make Traefik
	// pick a router arbitrarily.
	mappings, err := m.List(composeContent)
	if err != nil {
		return "", err
	}
	for _, mp := range mappings {
		if strings.EqualFold(mp.Domain, domain) {
			return "", fmt.Errorf("domain %q is already routed to service %q", domain, mp.Service)
		}
	}

	labels.set("traefik.enable", "true")
	labels.set(ruleKey, fmt.Sprintf("Host(`%s`)", domain))
	labels.set(fmt.Sprintf("traefik.http.routers.%s.entrypoints", router), "websecure")
	labels.set(fmt.Sprintf("traefik.http.routers.%s.tls.certresolver", router), "letsencrypt")
	labels.set(fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", router), strconv.Itoa(port))

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(out), nil
}

// Remove removes the Traefik router for the given domain from the specified service.
//
// The router's rule must be exactly Host(`domain`); otherwise nothing is
// changed and an error is returned, so a typo or a rule covering other
// hosts never triggers a redeploy that drops routing. Other labels keep
// their order and form.
func (m *TraefikManager) Remove(composeContent, service, domain string) (string, error) {
	if err := validateRoute(service, domain); err != nil {
		return "", err
	}

	doc, svcNode, err := findServiceNode(composeContent, service)
	if err != nil {
		return "", err
	}
	labels, err := editableLabels(doc.Content[0], svcNode, service)
	if err != nil {
		return "", err
	}

	router := m.routerName(service)
	ruleKey := fmt.Sprintf("traefik.http.routers.%s.rule", router)
	rule, ok := labels.get(ruleKey)
	if !ok {
		return "", fmt.Errorf("service %q has no Traefik route managed by Towline (no %s label); nothing to remove", service, ruleKey)
	}
	host, single := singleHostRule(rule)
	if !single {
		if strings.Contains(strings.ToLower(rule), strings.ToLower(domain)) {
			return "", fmt.Errorf("the route of service %q is %q, which is not a single Host rule; edit the labels in the compose file to remove %q", service, rule, domain)
		}
		return "", fmt.Errorf("domain %q not found on service %q (rule is %q)", domain, service, rule)
	}
	if !strings.EqualFold(host, domain) {
		return "", fmt.Errorf("domain %q not found on service %q (it routes %q)", domain, service, host)
	}

	// Remove all labels associated with this router
	routerPrefix := fmt.Sprintf("traefik.http.routers.%s.", router)
	servicePrefix := fmt.Sprintf("traefik.http.services.%s.", router)
	labels.deleteMatching(func(k string) bool {
		return strings.HasPrefix(k, routerPrefix) || strings.HasPrefix(k, servicePrefix)
	})

	// If no traefik labels remain, remove traefik.enable too
	hasTraefikLabels := false
	for _, k := range labels.keys() {
		if strings.HasPrefix(k, "traefik.http.") {
			hasTraefikLabels = true
			break
		}
	}
	if !hasTraefikLabels {
		labels.deleteMatching(func(k string) bool { return k == "traefik.enable" })
	}
	labels.dropIfEmpty()

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
