package towline

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripDockerLogHeaders(t *testing.T) {
	// Build a Docker multiplexed log frame:
	// Header: [stream_type=1 (stdout)][0][0][0][size=13 (big-endian)][payload="Hello, World\n"]
	payload := []byte("Hello, World\n")
	frame := make([]byte, 8+len(payload))
	frame[0] = 1 // stdout
	frame[1] = 0
	frame[2] = 0
	frame[3] = 0
	// Size in big-endian
	size := len(payload)
	frame[4] = byte(size >> 24)
	frame[5] = byte(size >> 16)
	frame[6] = byte(size >> 8)
	frame[7] = byte(size)
	copy(frame[8:], payload)

	result := stripDockerLogHeaders(frame)
	assert.Equal(t, "Hello, World\n", result)
}

func TestStripDockerLogHeaders_MultipleFrames(t *testing.T) {
	// Two frames: stdout "line1\n" and stderr "error\n"
	buildFrame := func(streamType byte, payload string) []byte {
		data := []byte(payload)
		frame := make([]byte, 8+len(data))
		frame[0] = streamType
		size := len(data)
		frame[4] = byte(size >> 24)
		frame[5] = byte(size >> 16)
		frame[6] = byte(size >> 8)
		frame[7] = byte(size)
		copy(frame[8:], data)
		return frame
	}

	var combined []byte
	combined = append(combined, buildFrame(1, "line1\n")...)
	combined = append(combined, buildFrame(2, "error\n")...)

	result := stripDockerLogHeaders(combined)
	assert.Equal(t, "line1\nerror\n", result)
}

func TestStripDockerLogHeaders_EmptyInput(t *testing.T) {
	result := stripDockerLogHeaders([]byte{})
	assert.Equal(t, "", result)
}

func TestStripDockerLogHeaders_ShortInput(t *testing.T) {
	// Less than 8 bytes: treated as raw data
	result := stripDockerLogHeaders([]byte("short"))
	assert.Equal(t, "short", result)
}

func TestStripDockerLogHeaders_Table(t *testing.T) {
	frame := func(streamType byte, payload string) []byte {
		f := make([]byte, 8+len(payload))
		f[0] = streamType
		n := len(payload)
		f[4], f[5], f[6], f[7] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
		copy(f[8:], payload)
		return f
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{name: "TTY output with long lines is returned raw", in: []byte("2026-01-01 server listening on :8080\nGET /health 200\n"), want: "2026-01-01 server listening on :8080\nGET /health 200\n"},
		{name: "TTY output with ANSI colors is returned raw", in: []byte("\x1b[32mINFO\x1b[0m ready\n"), want: "\x1b[32mINFO\x1b[0m ready\n"},
		{name: "TTY output starting with control byte but nonzero padding", in: []byte{1, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'}, want: string([]byte{1, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'})},
		{name: "stdout and stderr frames", in: cat(frame(1, "out\n"), frame(2, "err\n")), want: "out\nerr\n"},
		{name: "stdin stream type 0", in: frame(0, "in\n"), want: "in\n"},
		{name: "truncated final frame payload", in: cat(frame(1, "a\n"), frame(1, "partial line\n")[:12]), want: "a\npart"},
		{name: "garbage after valid frames kept raw", in: cat(frame(1, "a\n"), []byte("tail text")), want: "a\ntail text"},
		{name: "empty", in: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripDockerLogHeaders(tt.in))
		})
	}
}

func TestParseLogsSince(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty", in: "", want: ""},
		{name: "unix seconds pass through", in: "1700000000", want: "1700000000"},
		{name: "unix seconds with fraction pass through", in: "1700000000.5", want: "1700000000.5"},
		{name: "RFC3339 UTC", in: "2026-10-08T11:00:00Z", want: strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)},
		{name: "RFC3339 with offset", in: "2026-10-08T13:00:00+02:00", want: strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)},
		{name: "RFC3339 with fractional seconds", in: "2026-10-08T11:00:00.123Z", want: strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)},
		{name: "duration minutes", in: "10m", want: strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)},
		{name: "duration compound", in: "1h30m", want: strconv.FormatInt(now.Add(-90*time.Minute).Unix(), 10)},
		{name: "surrounding whitespace", in: " 10m ", want: strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)},
		{name: "negative duration", in: "-5m", wantErr: true},
		{name: "zero duration", in: "0s", wantErr: true},
		{name: "date only", in: "2026-10-08", wantErr: true},
		{name: "garbage", in: "yesterday", wantErr: true},
		{name: "query injection", in: "1&until=2", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLogsSince(tt.in, now)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandleServiceLogs_Since(t *testing.T) {
	tests := []struct {
		name      string
		since     string
		wantErr   string
		wantQuery func(t *testing.T, v string)
	}{
		{
			name:  "RFC3339 is sent as unix seconds",
			since: "2026-01-02T15:04:05Z",
			wantQuery: func(t *testing.T, v string) {
				assert.Equal(t, "1767366245", v)
			},
		},
		{
			name:  "duration is sent as unix seconds",
			since: "10m",
			wantQuery: func(t *testing.T, v string) {
				n, err := strconv.ParseInt(v, 10, 64)
				require.NoError(t, err)
				assert.InDelta(t, time.Now().Add(-10*time.Minute).Unix(), n, 5)
			},
		},
		{name: "invalid is rejected before calling Docker", since: "last tuesday", wantErr: "invalid since"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotSince string
			var sawLogs bool
			proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
				if opts.Path == "/containers/json" {
					return containersJSON(testStackName,
						struct{ id, service, state, status string }{"abc123def456", "web", "running", "Up"},
					), nil
				}
				sawLogs = true
				gotSince = opts.QueryParams["since"]
				return []byte("tty line\n"), nil
			}
			h := setupHandlers(t, new(mockPortainerClient), proxyFn)
			result, err := h.HandleServiceLogs()(context.Background(), newRequest(map[string]any{"service": "web", "since": tt.since}))
			require.NoError(t, err)
			if tt.wantErr != "" {
				assert.True(t, result.IsError)
				assert.Contains(t, resultText(t, result), tt.wantErr)
				assert.False(t, sawLogs)
				return
			}
			assert.False(t, result.IsError)
			assert.Equal(t, "tty line\n", resultText(t, result))
			tt.wantQuery(t, gotSince)
		})
	}
}

func TestFilterLines(t *testing.T) {
	text := "INFO starting server\nERROR connection failed\nINFO request handled\nWARN slow query\n"

	result := filterLines(text, "ERROR")
	assert.Equal(t, "ERROR connection failed\n", result)

	result = filterLines(text, "INFO")
	assert.Equal(t, "INFO starting server\nINFO request handled\n", result)

	result = filterLines(text, "NOTFOUND")
	assert.Equal(t, "", result)
}
