package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ServiceHealth represents the health status of a compose service container.
type ServiceHealth struct {
	Service       string  `json:"service"`
	State         string  `json:"state"`
	Uptime        string  `json:"uptime"`
	RestartCount  int     `json:"restart_count"`
	CpuPercent    float64 `json:"cpu_percent"`
	MemoryUsageMB float64 `json:"memory_usage_mb"`
	MemoryLimitMB float64 `json:"memory_limit_mb"`
	ExitCode      *int    `json:"exit_code,omitempty"`
	StatsError    string  `json:"stats_error,omitempty"`
	InspectError  string  `json:"inspect_error,omitempty"`
}

// containerStats is the parsed Docker stats JSON.
type containerStats struct {
	CPUPercent    float64
	MemoryUsageMB float64
	MemoryLimitMB float64
}

// parseContainerStats parses Docker /containers/{id}/stats?stream=false response
// and calculates CPU percentage.
func parseContainerStats(data []byte) (*containerStats, error) {
	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage float64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage float64 `json:"system_cpu_usage"`
			OnlineCPUs     float64 `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage float64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage float64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage float64 `json:"usage"`
			Limit float64 `json:"limit"`
		} `json:"memory_stats"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse container stats: %w", err)
	}

	// Calculate CPU percentage
	cpuDelta := raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage
	systemDelta := raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage

	var cpuPercent float64
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * raw.CPUStats.OnlineCPUs * 100.0
	}

	// Round to 2 decimal places
	cpuPercent = math.Round(cpuPercent*100) / 100

	const bytesToMB = 1024 * 1024
	return &containerStats{
		CPUPercent:    cpuPercent,
		MemoryUsageMB: math.Round(raw.MemoryStats.Usage/bytesToMB*100) / 100,
		MemoryLimitMB: math.Round(raw.MemoryStats.Limit/bytesToMB*100) / 100,
	}, nil
}

// parseInspectInfo extracts restart count, uptime, and exit code from Docker inspect JSON.
func parseInspectInfo(data []byte) (restartCount int, uptime string, exitCode *int) {
	var raw struct {
		State struct {
			StartedAt  string `json:"StartedAt"`
			FinishedAt string `json:"FinishedAt"`
			ExitCode   int    `json:"ExitCode"`
			Running    bool   `json:"Running"`
		} `json:"State"`
		RestartCount int `json:"RestartCount"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, "unknown", nil
	}

	restartCount = raw.RestartCount

	if raw.State.Running {
		if startedAt, err := time.Parse(time.RFC3339Nano, raw.State.StartedAt); err == nil {
			uptime = time.Since(startedAt).Truncate(time.Second).String()
		} else {
			uptime = "unknown"
		}
	} else {
		uptime = "stopped"
		ec := raw.State.ExitCode
		exitCode = &ec
	}

	return restartCount, uptime, exitCode
}

// HandleServiceHealth returns a handler for the towline_service_health tool.
func (h *Handlers) HandleServiceHealth() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid service parameter", err), nil
		}

		var containers []ContainerInfo
		if service != "" {
			containers, err = ResolveService(h.proxyFn, h.EnvID, h.StackName, service)
		} else {
			containers, err = ResolveAllServices(h.proxyFn, h.EnvID, h.StackName)
		}
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to resolve services", err), nil
		}

		if len(containers) == 0 {
			return mcp.NewToolResultText("[]"), nil
		}

		var healthResults []ServiceHealth

		for _, c := range containers {
			health := ServiceHealth{
				Service: c.Service,
				State:   c.State,
			}

			// Get stats
			statsData, err := h.proxyFn(models.DockerProxyRequestOptions{
				EnvironmentID: h.EnvID,
				Method:        "GET",
				Path:          fmt.Sprintf("/containers/%s/stats", c.ID),
				QueryParams:   map[string]string{"stream": "false"},
			})
			if err != nil {
				health.StatsError = err.Error()
			} else if stats, parseErr := parseContainerStats(statsData); parseErr != nil {
				health.StatsError = parseErr.Error()
			} else {
				health.CpuPercent = stats.CPUPercent
				health.MemoryUsageMB = stats.MemoryUsageMB
				health.MemoryLimitMB = stats.MemoryLimitMB
			}

			// Get inspect info
			inspectData, err := h.proxyFn(models.DockerProxyRequestOptions{
				EnvironmentID: h.EnvID,
				Method:        "GET",
				Path:          fmt.Sprintf("/containers/%s/json", c.ID),
			})
			if err != nil {
				health.InspectError = err.Error()
			} else {
				health.RestartCount, health.Uptime, health.ExitCode = parseInspectInfo(inspectData)
			}

			healthResults = append(healthResults, health)
		}

		jsonData, err := json.Marshal(healthResults)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to marshal health results", err), nil
		}

		return mcp.NewToolResultText(string(jsonData)), nil
	}
}
