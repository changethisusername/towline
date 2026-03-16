package towline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContainerStats(t *testing.T) {
	data := []byte(`{
		"cpu_stats": {
			"cpu_usage": {
				"total_usage": 2000000000
			},
			"system_cpu_usage": 20000000000,
			"online_cpus": 4
		},
		"precpu_stats": {
			"cpu_usage": {
				"total_usage": 1000000000
			},
			"system_cpu_usage": 10000000000
		},
		"memory_stats": {
			"usage": 104857600,
			"limit": 2147483648
		}
	}`)

	stats, err := parseContainerStats(data)
	require.NoError(t, err)

	// CPU: (2e9 - 1e9) / (2e10 - 1e10) * 4 * 100 = 40%
	assert.InDelta(t, 40.0, stats.CPUPercent, 0.01)

	// Memory: 104857600 / (1024*1024) = 100 MB
	assert.InDelta(t, 100.0, stats.MemoryUsageMB, 0.01)

	// Memory limit: 2147483648 / (1024*1024) = 2048 MB
	assert.InDelta(t, 2048.0, stats.MemoryLimitMB, 0.01)
}

func TestParseContainerStats_ZeroDelta(t *testing.T) {
	data := []byte(`{
		"cpu_stats": {
			"cpu_usage": {"total_usage": 1000},
			"system_cpu_usage": 1000,
			"online_cpus": 4
		},
		"precpu_stats": {
			"cpu_usage": {"total_usage": 1000},
			"system_cpu_usage": 1000
		},
		"memory_stats": {"usage": 0, "limit": 0}
	}`)

	stats, err := parseContainerStats(data)
	require.NoError(t, err)
	assert.Equal(t, 0.0, stats.CPUPercent)
}

func TestParseContainerStats_InvalidJSON(t *testing.T) {
	_, err := parseContainerStats([]byte(`not json`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse container stats")
}

func TestParseInspectInfo_Running(t *testing.T) {
	data := []byte(`{
		"State": {
			"StartedAt": "2026-03-17T10:00:00.000000000Z",
			"FinishedAt": "0001-01-01T00:00:00Z",
			"ExitCode": 0,
			"Running": true
		},
		"RestartCount": 3
	}`)

	restartCount, uptime, exitCode := parseInspectInfo(data)
	assert.Equal(t, 3, restartCount)
	assert.NotEqual(t, "unknown", uptime)
	assert.NotEqual(t, "stopped", uptime)
	assert.Nil(t, exitCode)
}

func TestParseInspectInfo_Stopped(t *testing.T) {
	data := []byte(`{
		"State": {
			"StartedAt": "2026-03-17T10:00:00.000000000Z",
			"FinishedAt": "2026-03-17T11:00:00.000000000Z",
			"ExitCode": 137,
			"Running": false
		},
		"RestartCount": 1
	}`)

	restartCount, uptime, exitCode := parseInspectInfo(data)
	assert.Equal(t, 1, restartCount)
	assert.Equal(t, "stopped", uptime)
	require.NotNil(t, exitCode)
	assert.Equal(t, 137, *exitCode)
}

func TestParseInspectInfo_InvalidJSON(t *testing.T) {
	restartCount, uptime, exitCode := parseInspectInfo([]byte(`bad`))
	assert.Equal(t, 0, restartCount)
	assert.Equal(t, "unknown", uptime)
	assert.Nil(t, exitCode)
}
