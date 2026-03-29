package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var containerPathRegex = regexp.MustCompile(`^/containers/([^/]+)(?:/|$)`)

func extractContainerID(path string) string {
	matches := containerPathRegex.FindStringSubmatch(path)
	if len(matches) < 2 {
		return ""
	}
	id := matches[1]
	if id == "json" || id == "create" {
		return ""
	}
	return id
}

// ContainerOwnership enforces that Docker proxy calls only target containers
// belonging to the scoped stack.
type ContainerOwnership struct {
	stackName string
	envID     int
	proxyFn   func(opts models.DockerProxyRequestOptions) ([]byte, error)
}

// NewContainerOwnership creates a new ContainerOwnership middleware.
func NewContainerOwnership(stackName string, envID int, proxyFn func(opts models.DockerProxyRequestOptions) ([]byte, error)) *ContainerOwnership {
	return &ContainerOwnership{stackName: stackName, envID: envID, proxyFn: proxyFn}
}

// blockedDockerPaths lists Docker API path prefixes that are blocked for non-GET
// requests. These endpoints can affect resources outside the scoped stack.
var blockedDockerPaths = []string{
	"/networks/",
	"/volumes/",
	"/swarm/",
	"/secrets/",
	"/configs/",
	"/plugins/",
	"/services/",
	"/nodes/",
	"/images/create",    // image pull
	"/images/build",     // image build
	"/images/push",      // image push
	"/build",            // build
	"/system/",          // system prune, info mutations
	"/containers/prune", // host-wide container prune
}

// isBlockedPath returns true if the path is a mutating request to a blocked Docker API endpoint.
func isBlockedPath(path, method string) bool {
	if method == "GET" || method == "HEAD" {
		return false
	}
	for _, prefix := range blockedDockerPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// Block /exec/{id}/start — can target any exec instance, bypassing container ownership
	if strings.HasPrefix(path, "/exec/") && method != "GET" {
		return true
	}
	// Block DELETE on images — host-wide impact
	if strings.HasPrefix(path, "/images/") && method == "DELETE" {
		return true
	}
	return false
}

// ForDockerProxy returns a middleware function that enforces container ownership
// for Docker proxy tool calls.
func (o *ContainerOwnership) ForDockerProxy() MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			parser := toolgen.NewParameterParser(request)
			path, _ := parser.GetString("dockerAPIPath", false)
			method, _ := parser.GetString("method", false)

			// Block mutating calls to non-container endpoints
			if isBlockedPath(path, method) {
				return mcp.NewToolResultError(fmt.Sprintf("Docker API path %q is not allowed for method %s in scoped mode", path, method)), nil
			}

			containerID := extractContainerID(path)
			if containerID == "" {
				if strings.HasPrefix(path, "/containers/json") {
					return o.filterContainerList(ctx, request, next)
				}
				return next(ctx, request)
			}

			owned, err := o.checkOwnership(containerID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to verify container ownership: %v", err)), nil
			}
			if !owned {
				return mcp.NewToolResultError(fmt.Sprintf("container %s does not belong to stack '%s'", containerID, o.stackName)), nil
			}

			return next(ctx, request)
		}
	}
}

func (o *ContainerOwnership) checkOwnership(containerID string) (bool, error) {
	body, err := o.proxyFn(models.DockerProxyRequestOptions{
		EnvironmentID: o.envID,
		Method:        "GET",
		Path:          fmt.Sprintf("/containers/%s/json", containerID),
	})
	if err != nil {
		return false, err
	}

	var inspect struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(body, &inspect); err != nil {
		return false, fmt.Errorf("failed to parse inspect: %w", err)
	}

	return inspect.Config.Labels["com.docker.compose.project"] == o.stackName, nil
}

func (o *ContainerOwnership) filterContainerList(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	result, err := next(ctx, request)
	if err != nil {
		return result, err
	}

	if len(result.Content) == 0 {
		return result, nil
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return result, nil
	}
	text := tc.Text
	var containers []map[string]any
	if err := json.Unmarshal([]byte(text), &containers); err != nil {
		return result, nil
	}

	filtered := []map[string]any{}
	for _, c := range containers {
		labels, _ := c["Labels"].(map[string]any)
		if labels != nil {
			project, _ := labels["com.docker.compose.project"].(string)
			if project == o.stackName {
				filtered = append(filtered, c)
			}
		}
	}

	data, _ := json.Marshal(filtered)
	return mcp.NewToolResultText(string(data)), nil
}
