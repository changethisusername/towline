package towline

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// stripDockerLogHeaders removes the 8-byte multiplexed stream header that Docker
// prepends to each log line when using the Docker API (not TTY mode).
// Each frame has: [stream_type(1 byte)][0 0 0][size(4 bytes BE)][payload]
func stripDockerLogHeaders(data []byte) string {
	var result strings.Builder
	pos := 0

	for pos < len(data) {
		// Need at least 8 bytes for the header
		if pos+8 > len(data) {
			// Remaining bytes don't form a complete header; append as-is
			result.Write(data[pos:])
			break
		}

		// Read the frame size from bytes 4-7 (big-endian uint32)
		frameSize := int(data[pos+4])<<24 | int(data[pos+5])<<16 | int(data[pos+6])<<8 | int(data[pos+7])

		pos += 8 // Skip header

		if frameSize <= 0 {
			continue
		}

		end := pos + frameSize
		if end > len(data) {
			end = len(data)
		}

		result.Write(data[pos:end])
		pos = end
	}

	return result.String()
}

// HandleServiceLogs returns a handler for the towline_service_logs tool.
func (h *Handlers) HandleServiceLogs() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		tail, err := parser.GetInt("tail", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid tail parameter", err), nil
		}
		if tail <= 0 {
			tail = 200
		}

		since, err := parser.GetString("since", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid since parameter", err), nil
		}

		filter, err := parser.GetString("filter", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid filter parameter", err), nil
		}

		// Resolve service to container(s)
		containers, err := ResolveService(h.proxyFn, h.EnvID, h.StackName, service)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to resolve service", err), nil
		}

		var allLogs strings.Builder

		for _, c := range containers {
			queryParams := map[string]string{
				"stdout": "true",
				"stderr": "true",
				"tail":   strconv.Itoa(tail),
			}
			if since != "" {
				queryParams["since"] = since
			}

			data, err := h.proxyFn(models.DockerProxyRequestOptions{
				EnvironmentID: h.EnvID,
				Method:        "GET",
				Path:          fmt.Sprintf("/containers/%s/logs", c.ID),
				QueryParams:   queryParams,
			})
			if err != nil {
				allLogs.WriteString(fmt.Sprintf("--- %s (error: %s) ---\n", c.Service, err.Error()))
				continue
			}

			logText := stripDockerLogHeaders(data)

			// Apply grep-like filter if specified
			if filter != "" {
				logText = filterLines(logText, filter)
			}

			if len(containers) > 1 {
				allLogs.WriteString(fmt.Sprintf("--- %s (%s) ---\n", c.Service, truncateID(c.ID)))
			}
			allLogs.WriteString(logText)
		}

		return mcp.NewToolResultText(allLogs.String()), nil
	}
}

// filterLines returns only lines containing the filter substring.
func filterLines(text, filter string) string {
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, filter) {
			result.WriteString(line)
			result.WriteByte('\n')
		}
	}
	return result.String()
}
