package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// CaddyManager manages domain routing via the Caddy admin API.
type CaddyManager struct {
	AdminURL string
	client   *http.Client
}

// NewCaddyManager creates a new CaddyManager with the given admin API URL.
func NewCaddyManager(adminURL string) *CaddyManager {
	return &CaddyManager{
		AdminURL: adminURL,
		client:   &http.Client{},
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

// routeID generates a consistent route ID for a service+domain combination.
func routeID(service, domain string) string {
	return fmt.Sprintf("towline-%s-%s", service, domain)
}

// Add creates a new Caddy route via the admin API.
// The composeContent parameter is ignored for Caddy (routes are managed via API, not compose labels).
func (m *CaddyManager) Add(composeContent, service, domain string, port int) (string, error) {
	route := caddyRoute{
		ID: routeID(service, domain),
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

	url := fmt.Sprintf("%s/config/apps/http/servers/srv0/routes", m.AdminURL)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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

	// Return compose content unchanged — Caddy routes are managed externally
	return composeContent, nil
}

// Remove deletes a Caddy route by its ID via the admin API.
func (m *CaddyManager) Remove(composeContent, service, domain string) (string, error) {
	id := routeID(service, domain)
	url := fmt.Sprintf("%s/id/%s", m.AdminURL, id)

	req, err := http.NewRequest("DELETE", url, nil)
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

// List retrieves all Caddy routes and extracts domain mappings from towline-managed routes.
func (m *CaddyManager) List(composeContent string) ([]DomainMapping, error) {
	url := fmt.Sprintf("%s/config/apps/http/servers/srv0/routes", m.AdminURL)

	resp, err := m.client.Get(url)
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

	return ParseCaddyRoutes(routes), nil
}

// ParseCaddyRoutes extracts DomainMappings from a list of Caddy routes.
// Exported for testing.
func ParseCaddyRoutes(routes []caddyRoute) []DomainMapping {
	var mappings []DomainMapping

	for _, route := range routes {
		// Only process towline-managed routes
		if !strings.HasPrefix(route.ID, "towline-") {
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
