package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CaddyManager manages domain routing via the Caddy admin API.
//
// The Caddy instance is shared between projects, so every route this manager
// creates, lists, or removes is namespaced by stack name, and a hostname that
// is already routed elsewhere cannot be claimed.
type CaddyManager struct {
	AdminURL  string
	StackName string
	client    *http.Client
}

// NewCaddyManager creates a new CaddyManager for a stack with the given admin API URL.
func NewCaddyManager(adminURL, stackName string) *CaddyManager {
	return &CaddyManager{
		AdminURL:  strings.TrimRight(adminURL, "/"),
		StackName: stackName,
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

// caddyRoute represents a Caddy route configuration.
type caddyRoute struct {
	ID     string        `json:"@id,omitempty"`
	Match  []caddyMatch  `json:"match,omitempty"`
	Handle []caddyHandle `json:"handle,omitempty"`
}

// caddyMatch represents a Caddy route matcher.
type caddyMatch struct {
	Host []string `json:"host,omitempty"`
}

// caddyHandle represents a Caddy route handler.
type caddyHandle struct {
	Handler   string          `json:"handler"`
	Upstreams []caddyUpstream `json:"upstreams,omitempty"`
}

// caddyUpstream represents a Caddy reverse proxy upstream.
type caddyUpstream struct {
	Dial string `json:"dial"`
}

// routeIDPrefix returns the prefix shared by all route IDs for a stack.
// ':' cannot appear in stack, service, or domain names, so prefixes of
// different stacks never overlap.
func routeIDPrefix(stackName string) string {
	return "towline:" + stackName + ":"
}

// routeID generates a consistent route ID for a stack+service+domain combination.
func routeID(stackName, service, domain string) string {
	return fmt.Sprintf("%s%s:%s", routeIDPrefix(stackName), service, domain)
}

func validateRoute(service, domain string) error {
	if !domainRegex.MatchString(domain) {
		return fmt.Errorf("invalid domain %q: must be a valid hostname", domain)
	}
	if !serviceNameRegex.MatchString(service) {
		return fmt.Errorf("invalid service name %q: must contain only letters, digits, hyphens, and underscores", service)
	}
	return nil
}

// Add creates a new Caddy route via the admin API.
// The service must exist in this stack's compose file, and the domain must not
// already be routed by another route. The compose content is returned unchanged
// (Caddy routes are managed via API, not compose labels).
func (m *CaddyManager) Add(composeContent, service, domain string, port int) (string, error) {
	if err := validateRoute(service, domain); err != nil {
		return "", err
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid port %d", port)
	}
	if _, _, err := findServiceNode(composeContent, service); err != nil {
		return "", err
	}

	id := routeID(m.StackName, service, domain)
	routes, err := m.getRoutes()
	if err != nil {
		return "", err
	}
	for _, r := range routes {
		if r.ID == id {
			return "", fmt.Errorf("route for %s -> %s already exists", domain, service)
		}
		for _, match := range r.Match {
			for _, host := range match.Host {
				if strings.EqualFold(host, domain) {
					return "", fmt.Errorf("domain %q is already routed by another route and cannot be claimed by stack %q", domain, m.StackName)
				}
			}
		}
	}

	route := caddyRoute{
		ID: id,
		Match: []caddyMatch{
			{Host: []string{domain}},
		},
		Handle: []caddyHandle{
			{
				Handler: "reverse_proxy",
				Upstreams: []caddyUpstream{
					{Dial: fmt.Sprintf("%s:%d", service, port)},
				},
			},
		},
	}

	body, err := json.Marshal(route)
	if err != nil {
		return "", fmt.Errorf("failed to marshal route: %w", err)
	}

	req, err := http.NewRequest("POST", m.routesURL(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to add Caddy route: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Caddy API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return composeContent, nil
}

// Remove deletes a Caddy route by its ID via the admin API.
// Only routes belonging to this stack can be addressed.
func (m *CaddyManager) Remove(composeContent, service, domain string) (string, error) {
	if err := validateRoute(service, domain); err != nil {
		return "", err
	}
	id := routeID(m.StackName, service, domain)
	req, err := http.NewRequest("DELETE", m.AdminURL+"/id/"+url.PathEscape(id), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to remove Caddy route: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Caddy API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return composeContent, nil
}

// List retrieves this stack's Caddy routes and extracts domain mappings.
func (m *CaddyManager) List(composeContent string) ([]DomainMapping, error) {
	routes, err := m.getRoutes()
	if err != nil {
		return nil, err
	}
	return ParseCaddyRoutes(routes, routeIDPrefix(m.StackName)), nil
}

func (m *CaddyManager) routesURL() string {
	return m.AdminURL + "/config/apps/http/servers/srv0/routes"
}

// getRoutes fetches all routes configured on the Caddy server.
func (m *CaddyManager) getRoutes() ([]caddyRoute, error) {
	resp, err := m.client.Get(m.routesURL())
	if err != nil {
		return nil, fmt.Errorf("failed to get Caddy routes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Caddy API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var routes []caddyRoute
	if err := json.Unmarshal(body, &routes); err != nil {
		return nil, fmt.Errorf("failed to parse routes: %w", err)
	}
	return routes, nil
}

// ParseCaddyRoutes extracts DomainMappings from a list of Caddy routes.
// Only routes whose ID starts with idPrefix are included.
// Exported for testing.
func ParseCaddyRoutes(routes []caddyRoute, idPrefix string) []DomainMapping {
	var mappings []DomainMapping

	for _, route := range routes {
		if !strings.HasPrefix(route.ID, idPrefix) {
			continue
		}

		if len(route.Match) == 0 || len(route.Match[0].Host) == 0 {
			continue
		}

		domain := route.Match[0].Host[0]

		var service string
		var port int

		if len(route.Handle) > 0 && len(route.Handle[0].Upstreams) > 0 {
			dial := route.Handle[0].Upstreams[0].Dial
			parts := strings.SplitN(dial, ":", 2)
			if len(parts) == 2 {
				service = parts[0]
				if p, err := strconv.Atoi(parts[1]); err == nil {
					port = p
				}
			} else if len(parts) == 1 {
				service = parts[0]
			}
		}

		mappings = append(mappings, DomainMapping{
			Service: service,
			Domain:  domain,
			Port:    port,
			Method:  BackendCaddy,
		})
	}

	return mappings
}
