package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var containerPathRegex = regexp.MustCompile(`^/containers/([^/]+)(?:/|$)`)

// extractContainerID returns the container identifier from a normalized
// /containers/{id}[/...] path, or "" for collection endpoints.
func extractContainerID(path string) string {
	matches := containerPathRegex.FindStringSubmatch(path)
	if len(matches) < 2 {
		return ""
	}
	id := matches[1]
	if reservedContainerIDs[id] {
		return ""
	}
	return id
}

// EnvResolver holds the scoped stack's environment ID. While the ID is
// unknown (the stack does not exist yet, or Portainer was unreachable at
// startup) each use looks it up again; once found it is cached.
type EnvResolver struct {
	mu     sync.Mutex
	id     int
	lookup func() int

	// bindMu guards the int fields kept in sync by Bind.
	bindMu sync.RWMutex
}

// NewEnvResolver returns a resolver starting at id (0 if unknown). lookup
// returns the current environment ID, or 0 if it cannot be determined.
func NewEnvResolver(id int, lookup func() int) *EnvResolver {
	return &EnvResolver{id: id, lookup: lookup}
}

// EnvID returns the environment ID, looking it up if it is still unknown.
// It returns 0 while the ID cannot be determined.
func (r *EnvResolver) EnvID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.id <= 0 && r.lookup != nil {
		if id := r.lookup(); id > 0 {
			r.id = id
		}
	}
	return r.id
}

// Bind returns a middleware that copies the resolved environment ID into
// *dst before each call (for handlers that read a plain int field) and runs
// the call under a read lock, so the write never races a handler's read.
// All handlers reading *dst must be wrapped with the same resolver.
func (r *EnvResolver) Bind(dst *int) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if id := r.EnvID(); id > 0 {
				r.bindMu.RLock()
				stale := *dst != id
				r.bindMu.RUnlock()
				if stale {
					r.bindMu.Lock()
					*dst = id
					r.bindMu.Unlock()
				}
			}
			r.bindMu.RLock()
			defer r.bindMu.RUnlock()
			return next(ctx, request)
		}
	}
}

// ContainerOwnership enforces that Docker proxy calls only target containers
// belonging to the scoped stack.
type ContainerOwnership struct {
	stackName string
	env       *EnvResolver
	proxyFn   func(opts models.DockerProxyRequestOptions) ([]byte, error)
}

// OwnershipOption configures a ContainerOwnership.
type OwnershipOption func(*ContainerOwnership)

// WithEnvResolver makes ownership checks use r for the environment ID, so an
// ID that was unknown at startup is picked up once the stack exists.
func WithEnvResolver(r *EnvResolver) OwnershipOption {
	return func(o *ContainerOwnership) { o.env = r }
}

// NewContainerOwnership creates a new ContainerOwnership middleware.
func NewContainerOwnership(stackName string, envID int, proxyFn func(opts models.DockerProxyRequestOptions) ([]byte, error), opts ...OwnershipOption) *ContainerOwnership {
	o := &ContainerOwnership{stackName: stackName, env: NewEnvResolver(envID, nil), proxyFn: proxyFn}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// ForDockerProxy returns a middleware function that enforces container ownership
// for Docker proxy tool calls.
func (o *ContainerOwnership) ForDockerProxy() MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			parser := toolgen.NewParameterParser(request)
			rawPath, _ := parser.GetString("dockerAPIPath", false)
			method, _ := parser.GetString("method", false)

			// Ownership is checked in this stack's environment, so the request
			// must go to that same environment; otherwise a container name the
			// stack owns here could address another project's container there.
			stackEnvID := o.env.EnvID()
			if stackEnvID > 0 {
				envID, err := parser.GetInt("environmentId", true)
				if err != nil {
					return mcp.NewToolResultErrorFromErr("invalid environmentId parameter", err), nil
				}
				if envID != stackEnvID {
					return mcp.NewToolResultError(fmt.Sprintf("environment %d is outside this stack's scope (environment %d)", envID, stackEnvID)), nil
				}
			}

			path, err := NormalizeDockerPath(rawPath)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Docker API path %q rejected: %v", rawPath, err)), nil
			}

			// Mutations are limited to lifecycle actions on the stack's own
			// containers; everything else (container create, exec, networks,
			// volumes, images, system, ...) can reach outside the stack.
			if !isReadMethod(method) && !isAllowedMutation(path, method) {
				return mcp.NewToolResultError(fmt.Sprintf("Docker API path %q is not allowed for method %s in scoped mode", rawPath, method)), nil
			}

			containerID := extractContainerID(path)
			if containerID == "" {
				if path == "/containers/json" {
					return o.filterContainerList(ctx, request, next)
				}
				return next(ctx, request)
			}

			owned, err := o.checkOwnership(stackEnvID, containerID)
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

func (o *ContainerOwnership) checkOwnership(envID int, containerID string) (bool, error) {
	body, err := o.proxyFn(models.DockerProxyRequestOptions{
		EnvironmentID: envID,
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
