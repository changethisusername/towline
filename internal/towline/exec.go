package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	execPollInterval = 500 * time.Millisecond
	execPollTimeout  = 30 * time.Second
)

// execCreateRequest is the body for Docker exec create API.
type execCreateRequest struct {
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Detach       bool     `json:"Detach"`
	Cmd          []string `json:"Cmd"`
}

// execCreateResponse is the response from Docker exec create API.
type execCreateResponse struct {
	ID string `json:"Id"`
}

// execInspectResponse is the response from Docker exec inspect API.
type execInspectResponse struct {
	Running  bool `json:"Running"`
	ExitCode int  `json:"ExitCode"`
}

// HandleExec returns a handler for the towline_exec tool.
// It resolves the service, creates an exec instance, starts it, and polls for completion.
func (h *Handlers) HandleExec() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		command, err := parser.GetString("command", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid command parameter", err), nil
		}

		// Resolve service to a container
		containers, err := ResolveService(h.proxyFn, h.EnvID, h.StackName, service)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to resolve service", err), nil
		}

		// Use the first running container
		var containerID string
		for _, c := range containers {
			if c.State == "running" {
				containerID = c.ID
				break
			}
		}
		if containerID == "" {
			return mcp.NewToolResultError(fmt.Sprintf("no running container found for service %q", service)), nil
		}

		// Parse command into args (split by spaces, respecting basic quoting)
		cmd := parseCommand(command)

		// Create exec instance
		execReq := execCreateRequest{
			AttachStdout: true,
			AttachStderr: true,
			Detach:       true,
			Cmd:          cmd,
		}

		execCreateBody, err := json.Marshal(execReq)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to marshal exec request", err), nil
		}

		createData, err := h.proxyFn(models.DockerProxyRequestOptions{
			EnvironmentID: h.EnvID,
			Method:        "POST",
			Path:          fmt.Sprintf("/containers/%s/exec", containerID),
			Headers:       map[string]string{"Content-Type": "application/json"},
			Body:          strings.NewReader(string(execCreateBody)),
		})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to create exec instance", err), nil
		}

		var createResp execCreateResponse
		if err := json.Unmarshal(createData, &createResp); err != nil {
			return mcp.NewToolResultErrorFromErr("failed to parse exec create response", err), nil
		}

		if createResp.ID == "" {
			return mcp.NewToolResultError("exec create returned empty ID"), nil
		}

		// Start the exec instance
		startBody := `{"Detach": true}`
		_, err = h.proxyFn(models.DockerProxyRequestOptions{
			EnvironmentID: h.EnvID,
			Method:        "POST",
			Path:          fmt.Sprintf("/exec/%s/start", createResp.ID),
			Headers:       map[string]string{"Content-Type": "application/json"},
			Body:          strings.NewReader(startBody),
		})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to start exec instance", err), nil
		}

		// Poll for completion
		deadline := time.Now().Add(execPollTimeout)
		var exitCode int
		completed := false

		for time.Now().Before(deadline) {
			inspectData, err := h.proxyFn(models.DockerProxyRequestOptions{
				EnvironmentID: h.EnvID,
				Method:        "GET",
				Path:          fmt.Sprintf("/exec/%s/json", createResp.ID),
			})
			if err != nil {
				// Inspect may fail briefly after start; continue polling
				time.Sleep(execPollInterval)
				continue
			}

			var inspectResp execInspectResponse
			if err := json.Unmarshal(inspectData, &inspectResp); err != nil {
				time.Sleep(execPollInterval)
				continue
			}

			if !inspectResp.Running {
				exitCode = inspectResp.ExitCode
				completed = true
				break
			}

			time.Sleep(execPollInterval)
		}

		if !completed {
			return mcp.NewToolResultText(fmt.Sprintf(
				"Exec started (ID: %s) but did not complete within %s. Command may still be running.",
				createResp.ID[:12], execPollTimeout,
			)), nil
		}

		result := fmt.Sprintf("Command executed on %s (container %s). Exit code: %d",
			service, containerID[:12], exitCode)

		if exitCode != 0 {
			result += " (non-zero exit code indicates failure)"
		}

		return mcp.NewToolResultText(result), nil
	}
}

// parseCommand splits a command string into arguments, handling basic quoting.
func parseCommand(command string) []string {
	// Use shell-like splitting: wrap in sh -c for complex commands
	if strings.ContainsAny(command, "|&;><$`") {
		return []string{"sh", "-c", command}
	}

	// Simple space splitting for basic commands
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return []string{"sh", "-c", command}
	}
	return parts
}
