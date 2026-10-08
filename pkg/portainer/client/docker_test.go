package client

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyDockerRequest(t *testing.T) {
	tests := []struct {
		name         string
		opts         models.DockerProxyRequestOptions
		status       int
		respBody     string
		wantPath     string
		wantQuery    map[string]string
		wantHeaders  map[string]string
		wantReqBody  string
		wantMethod   string
		wantAPIKey   string
		wantRespBody string
		wantStatus   int
	}{
		{
			name: "GET request with query parameters",
			opts: models.DockerProxyRequestOptions{
				EnvironmentID: 1,
				Method:        "GET",
				Path:          "/images/json",
				QueryParams:   map[string]string{"all": "true", "filters": `{"label":["a=b"]}`},
			},
			status:       http.StatusOK,
			respBody:     `[{"Id":"img1"}]`,
			wantMethod:   "GET",
			wantPath:     "/api/endpoints/1/docker/images/json",
			wantQuery:    map[string]string{"all": "true", "filters": `{"label":["a=b"]}`},
			wantAPIKey:   "test-token",
			wantRespBody: `[{"Id":"img1"}]`,
			wantStatus:   http.StatusOK,
		},
		{
			name: "POST request with custom headers and body",
			opts: models.DockerProxyRequestOptions{
				EnvironmentID: 2,
				Method:        "POST",
				Path:          "/networks/create",
				Headers:       map[string]string{"Content-Type": "application/json", "X-Custom-Header": "value1"},
				Body:          bytes.NewBufferString(`{"Name": "my-network"}`),
			},
			status:       http.StatusCreated,
			respBody:     `{"Id": "net1"}`,
			wantMethod:   "POST",
			wantPath:     "/api/endpoints/2/docker/networks/create",
			wantHeaders:  map[string]string{"Content-Type": "application/json", "X-Custom-Header": "value1"},
			wantReqBody:  `{"Name": "my-network"}`,
			wantAPIKey:   "test-token",
			wantRespBody: `{"Id": "net1"}`,
			wantStatus:   http.StatusCreated,
		},
		{
			name: "caller cannot override the API key",
			opts: models.DockerProxyRequestOptions{
				EnvironmentID: 1,
				Method:        "GET",
				Path:          "/version",
				Headers:       map[string]string{"X-API-Key": "other-key"},
			},
			status:     http.StatusOK,
			wantMethod: "GET",
			wantPath:   "/api/endpoints/1/docker/version",
			wantAPIKey: "test-token",
			wantStatus: http.StatusOK,
		},
		{
			name: "question mark in path is escaped, not a query",
			opts: models.DockerProxyRequestOptions{
				EnvironmentID: 1,
				Method:        "GET",
				Path:          "/containers/abc?all=1/json",
			},
			status:     http.StatusNotFound,
			wantMethod: "GET",
			wantPath:   "/api/endpoints/1/docker/containers/abc?all=1/json",
			wantQuery:  map[string]string{},
			wantAPIKey: "test-token",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "missing leading slash is added",
			opts: models.DockerProxyRequestOptions{
				EnvironmentID: 3,
				Method:        "GET",
				Path:          "info",
			},
			status:     http.StatusOK,
			wantMethod: "GET",
			wantPath:   "/api/endpoints/3/docker/info",
			wantAPIKey: "test-token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.wantMethod, r.Method)
				assert.Equal(t, tt.wantPath, r.URL.Path)
				assert.Equal(t, tt.wantAPIKey, r.Header.Get("X-API-Key"))
				if tt.wantQuery != nil {
					assert.Len(t, r.URL.Query(), len(tt.wantQuery))
					for k, v := range tt.wantQuery {
						assert.Equal(t, v, r.URL.Query().Get(k))
					}
				}
				for k, v := range tt.wantHeaders {
					assert.Equal(t, v, r.Header.Get(k))
				}
				body, _ := io.ReadAll(r.Body)
				assert.Equal(t, tt.wantReqBody, string(body))
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.respBody))
			}))
			defer srv.Close()

			// httptest serves plain http: this is the case the SDK proxy
			// could not handle because it hardcoded https://.
			c := NewPortainerClient(srv.URL, "test-token")
			resp, err := c.ProxyDockerRequest(tt.opts)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRespBody, string(body))
		})
	}
}

func TestProxyDockerRequestTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tests := []struct {
		name    string
		skip    bool
		wantErr bool
	}{
		{name: "self-signed cert rejected by default", skip: false, wantErr: true},
		{name: "self-signed cert accepted with skip TLS verify", skip: true, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewPortainerClient(srv.URL, "test-token", WithSkipTLSVerify(tt.skip))
			resp, err := c.ProxyDockerRequest(models.DockerProxyRequestOptions{EnvironmentID: 1, Method: "GET", Path: "/version"})
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			resp.Body.Close()
		})
	}
}

func TestProxyDockerRequestContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{name: "deadline aborts a hanging request", timeout: 50 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()
			c := NewPortainerClient(srv.URL, "test-token")
			start := time.Now()
			_, err := c.ProxyDockerRequest(models.DockerProxyRequestOptions{EnvironmentID: 1, Method: "POST", Path: "/exec/x/start", Context: ctx})
			assert.Error(t, err)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(start), 5*time.Second)
		})
	}
}
