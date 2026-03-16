package towline

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/changethisusername/towline/pkg/portainer/models"
)

// ContainerInfo holds resolved container metadata from Docker API.
type ContainerInfo struct {
	ID      string   `json:"id"`
	Service string   `json:"service"`
	State   string   `json:"state"`
	Status  string   `json:"status"`
	Names   []string `json:"names"`
}

// dockerContainer is the subset of Docker /containers/json response we need.
type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

// parseContainersForStack parses a Docker /containers/json response and filters
// to containers belonging to the given compose project (stack) name.
func parseContainersForStack(data []byte, stackName string) ([]ContainerInfo, error) {
	var containers []dockerContainer
	if err := json.Unmarshal(data, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse containers response: %w", err)
	}

	var result []ContainerInfo
	for _, c := range containers {
		project := c.Labels["com.docker.compose.project"]
		if project != stackName {
			continue
		}

		service := c.Labels["com.docker.compose.service"]
		result = append(result, ContainerInfo{
			ID:      c.ID,
			Service: service,
			State:   c.State,
			Status:  c.Status,
			Names:   c.Names,
		})
	}

	return result, nil
}

// filterByService returns only containers matching the given service name.
func filterByService(containers []ContainerInfo, service string) []ContainerInfo {
	var result []ContainerInfo
	for _, c := range containers {
		if c.Service == service {
			result = append(result, c)
		}
	}
	return result
}

// ResolveService resolves a compose service name to its container(s) via Docker API.
func ResolveService(proxyFn ProxyFunc, envID int, stackName, service string) ([]ContainerInfo, error) {
	containers, err := ResolveAllServices(proxyFn, envID, stackName)
	if err != nil {
		return nil, err
	}

	matched := filterByService(containers, service)
	if len(matched) == 0 {
		return nil, fmt.Errorf("no containers found for service %q in stack %q", service, stackName)
	}
	return matched, nil
}

// ResolveAllServices resolves all containers belonging to a stack via Docker API.
func ResolveAllServices(proxyFn ProxyFunc, envID int, stackName string) ([]ContainerInfo, error) {
	// Build label filter for Docker API
	filterJSON, _ := json.Marshal(map[string][]string{
		"label": {fmt.Sprintf("com.docker.compose.project=%s", stackName)},
	})

	data, err := proxyFn(models.DockerProxyRequestOptions{
		EnvironmentID: envID,
		Method:        "GET",
		Path:          "/containers/json",
		QueryParams: map[string]string{
			"all":     "true",
			"filters": url.QueryEscape(string(filterJSON)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	return parseContainersForStack(data, stackName)
}
