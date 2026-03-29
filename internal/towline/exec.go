package towline

import (
	"context"
	"encoding/binary"
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

		// Create exec instance (Detach omitted so output is capturable)
		execReq := execCreateRequest{
			AttachStdout: true,
			AttachStderr: true,
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

		// Start the exec instance (attached — captures stdout/stderr)
		startBody := `{"Detach": false, "Tty": false}`
		outputData, err := h.proxyFn(models.DockerProxyRequestOptions{
			EnvironmentID: h.EnvID,
			Method:        "POST",
			Path:          fmt.Sprintf("/exec/%s/start", createResp.ID),
			Headers:       map[string]string{"Content-Type": "application/json"},
			Body:          strings.NewReader(startBody),
		})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to start exec instance", err), nil
		}

		// Demux the Docker stream to extract stdout/stderr output
		output := demuxDockerStream(outputData)

		// Poll for exit code
		deadline := time.Now().Add(execPollTimeout)
		var exitCode int
		completed := false

		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return mcp.NewToolResultError("exec cancelled: " + ctx.Err().Error()), nil
			default:
			}

			inspectData, err := h.proxyFn(models.DockerProxyRequestOptions{
				EnvironmentID: h.EnvID,
				Method:        "GET",
				Path:          fmt.Sprintf("/exec/%s/json", createResp.ID),
			})
			if err != nil {
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
			result := fmt.Sprintf(
				"Exec started (ID: %s) but did not complete within %s. Command may still be running.",
				truncateID(createResp.ID), execPollTimeout,
			)
			if output != "" {
				result += "\n\nPartial output:\n" + output
			}
			return mcp.NewToolResultText(result), nil
		}

		var result strings.Builder
		fmt.Fprintf(&result, "Command executed on %s (container %s). Exit code: %d\n",
			service, truncateID(containerID), exitCode)

		if exitCode != 0 {
			result.WriteString("(non-zero exit code indicates failure)\n")
		}

		if output != "" {
			result.WriteString("\nOutput:\n")
			result.WriteString(output)
		}

		return mcp.NewToolResultText(result.String()), nil
	}
}

// parseCommand splits a command string into arguments using shell-like lexing
// without invoking a shell. Pipe, redirect, and other shell metacharacters are
// treated as literal characters — commands requiring shell semantics must
// explicitly pass ["sh", "-c", "<command>"] via the Cmd field.
func parseCommand(command string) []string {
	parts := shellSplit(command)
	if len(parts) == 0 {
		return []string{"echo", "empty command"}
	}
	return parts
}

// shellSplit performs basic POSIX-like shell lexing: it splits on whitespace,
// respects single and double quotes, and handles backslash escapes.
func shellSplit(s string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if (r == ' ' || r == '\t') && !inSingle && !inDouble {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// demuxDockerStream parses Docker's multiplexed stream format (used when Tty=false).
// Each frame has an 8-byte header: [stream_type, 0, 0, 0, size(4 bytes big-endian)]
// followed by the payload. Stream type 1 = stdout, 2 = stderr.
func demuxDockerStream(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var output strings.Builder
	i := 0
	for i+8 <= len(data) {
		size := binary.BigEndian.Uint32(data[i+4 : i+8])
		i += 8
		end := i + int(size)
		if end > len(data) {
			end = len(data)
		}
		output.Write(data[i:end])
		i = end
	}

	// If no valid frames were parsed, treat as raw text (e.g., Tty=true output)
	if output.Len() == 0 && len(data) > 0 {
		return string(data)
	}
	return strings.TrimRight(output.String(), "\n")
}

// truncateID safely truncates a Docker ID to 12 characters for display.
func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
