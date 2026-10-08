package towline

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dockerFrame(stream byte, payload string) []byte {
	f := make([]byte, 8+len(payload))
	f[0] = stream
	binary.BigEndian.PutUint32(f[4:8], uint32(len(payload)))
	copy(f[8:], payload)
	return f
}

// TestHandleExec_ThroughPortainerClient runs towline_exec against a fake
// Portainer over plain http, through the real client and NewProxyFunc, so
// it also covers the http scheme and the request context.
func TestHandleExec_ThroughPortainerClient(t *testing.T) {
	const (
		containerID = "abc123def456aa"
		execID      = "exec0123456789"
		base        = "/api/endpoints/1/docker"
	)

	tests := []struct {
		name        string
		hang        bool
		cancelTool  bool
		wantError   bool
		wantContain []string
	}{
		{
			name:        "command finishes",
			wantContain: []string{"Exit code: 0", "hello"},
		},
		{
			name:        "hung command times out with partial output",
			hang:        true,
			wantContain: []string{"did not finish within 200ms", "may still be running", "Partial output:\nhello"},
		},
		{
			name:        "tool call cancelled",
			hang:        true,
			cancelTool:  true,
			wantError:   true,
			wantContain: []string{"exec cancelled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(base+"/containers/json", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(containersJSON(testStackName,
					struct{ id, service, state, status string }{containerID, "web", "running", "Up"}))
			})
			mux.HandleFunc(base+"/containers/"+containerID+"/exec", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(execCreateResponse{ID: execID})
			})
			mux.HandleFunc(base+"/exec/"+execID+"/start", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(dockerFrame(1, "hello\n"))
				if tt.hang {
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
					case <-time.After(10 * time.Second):
					}
				}
			})
			mux.HandleFunc(base+"/exec/"+execID+"/json", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(execInspectResponse{Running: false, ExitCode: 0})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			pc := client.NewPortainerClient(srv.URL, "test-token")
			h := setupHandlers(t, new(mockPortainerClient), NewProxyFunc(pc.ProxyDockerRequest))
			h.execTimeout = 200 * time.Millisecond

			ctx := context.Background()
			if tt.cancelTool {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}

			start := time.Now()
			result, err := h.HandleExec()(ctx, newRequest(map[string]any{"service": "web", "command": "echo hello"}))
			require.NoError(t, err)
			assert.Less(t, time.Since(start), 5*time.Second, "exec must not block")
			assert.Equal(t, tt.wantError, result.IsError)
			text := resultText(t, result)
			for _, s := range tt.wantContain {
				assert.Contains(t, text, s)
			}
		})
	}
}

func TestHandleExec_HTTPErrorIsNotExitCodeZero(t *testing.T) {
	tests := []struct {
		name       string
		failPath   string
		wantSubstr string
	}{
		{name: "exec create 404", failPath: "/exec", wantSubstr: "failed to create exec instance"},
		{name: "exec start 409", failPath: "/start", wantSubstr: "failed to start exec instance"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const base = "/api/endpoints/1/docker"
			mux := http.NewServeMux()
			mux.HandleFunc(base+"/containers/json", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(containersJSON(testStackName,
					struct{ id, service, state, status string }{"cid000000000", "web", "running", "Up"}))
			})
			mux.HandleFunc(base+"/containers/cid000000000/exec", func(w http.ResponseWriter, r *http.Request) {
				if tt.failPath == "/exec" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"No such container: cid000000000"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(execCreateResponse{ID: "e1"})
			})
			mux.HandleFunc(base+"/exec/e1/start", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"container is paused"}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			pc := client.NewPortainerClient(srv.URL, "test-token")
			h := setupHandlers(t, new(mockPortainerClient), NewProxyFunc(pc.ProxyDockerRequest))
			result, err := h.HandleExec()(context.Background(), newRequest(map[string]any{"service": "web", "command": "true"}))
			require.NoError(t, err)
			assert.True(t, result.IsError)
			text := resultText(t, result)
			assert.Contains(t, text, tt.wantSubstr)
			assert.NotContains(t, text, "Exit code: 0")
		})
	}
}
