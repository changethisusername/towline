package towline

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// isDockerFrameHeader reports whether b starts with a multiplexed stream
// header: stream type 0 (stdin), 1 (stdout) or 2 (stderr), then three zero
// bytes. Log text never starts that way, so this distinguishes framed
// output from a TTY container's raw stream.
func isDockerFrameHeader(b []byte) bool {
	return len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream header that Docker
// prepends to each log line when using the Docker API (not TTY mode).
// Each frame has: [stream_type(1 byte)][0 0 0][size(4 bytes BE)][payload]
// Containers with a TTY send raw output without headers; that is returned
// unchanged, as is anything after a point where framing stops being valid.
func stripDockerLogHeaders(data []byte) string {
	if !isDockerFrameHeader(data) {
		return string(data)
	}

	var result strings.Builder
	pos := 0

	for pos < len(data) {
		if !isDockerFrameHeader(data[pos:]) {
			// Not a (complete) header; keep the remaining bytes as-is
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

		sinceArg, err := parser.GetString("since", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid since parameter", err), nil
		}
		since, err := parseLogsSince(sinceArg, time.Now())
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

// unixTimestampPattern matches what the Docker logs API accepts directly
// for since: unix seconds, optionally with a fractional part.
var unixTimestampPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,9})?$`)

// parseLogsSince converts the since argument into the unix timestamp the
// Docker logs API expects. It accepts an RFC3339 timestamp, a Go duration
// meaning "this long ago" (e.g. "10m", "1h30m"), or unix seconds, which
// are passed through. An empty value returns "".
func parseLogsSince(since string, now time.Time) (string, error) {
	since = strings.TrimSpace(since)
	if since == "" {
		return "", nil
	}
	if unixTimestampPattern.MatchString(since) {
		return since, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, since); err == nil {
		if t.Unix() < 0 {
			return "", fmt.Errorf("since %q is before 1970", since)
		}
		return strconv.FormatInt(t.Unix(), 10), nil
	}
	if d, err := time.ParseDuration(since); err == nil {
		if d <= 0 {
			return "", fmt.Errorf("since duration %q must be positive", since)
		}
		return strconv.FormatInt(now.Add(-d).Unix(), 10), nil
	}
	return "", fmt.Errorf("since %q must be an RFC3339 timestamp (2026-01-02T15:04:05Z), a duration such as 10m or 2h, or unix seconds", since)
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
