package towline

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/stretchr/testify/assert"
)

// errAfterReader returns data and then a read error, like a body cut off
// by an expired context.
type errAfterReader struct {
	r   io.Reader
	err error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		return n, e.err
	}
	return n, err
}

func TestNewProxyFunc(t *testing.T) {
	long := strings.Repeat("x", 2000)
	tests := []struct {
		name        string
		resp        *http.Response
		doErr       error
		wantData    string
		wantErr     bool
		errContains []string
		errExcludes string
	}{
		{
			name:     "200 returns body",
			resp:     &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"Id":"abc"}`))},
			wantData: `{"Id":"abc"}`,
		},
		{
			name:     "101 upgrade returns body",
			resp:     &http.Response{StatusCode: 101, Body: io.NopCloser(strings.NewReader("raw"))},
			wantData: "raw",
		},
		{
			name:        "404 docker error becomes error with message",
			resp:        &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"message":"No such container: abc"}`))},
			wantErr:     true,
			errContains: []string{"HTTP 404", "No such container: abc", "/containers/abc/json"},
		},
		{
			name:        "500 non-JSON body is quoted",
			resp:        &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("boom"))},
			wantErr:     true,
			errContains: []string{"HTTP 500", "boom"},
		},
		{
			name:        "403 empty body",
			resp:        &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(""))},
			wantErr:     true,
			errContains: []string{"HTTP 403", "(empty body)"},
		},
		{
			name:        "long error body is truncated",
			resp:        &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(long))},
			wantErr:     true,
			errContains: []string{"(truncated)"},
			errExcludes: strings.Repeat("x", maxProxyErrorBody+1),
		},
		{
			name:        "transport error passes through",
			doErr:       errors.New("connection refused"),
			wantErr:     true,
			errContains: []string{"connection refused"},
		},
		{
			name:        "read error keeps partial body",
			resp:        &http.Response{StatusCode: 200, Body: io.NopCloser(&errAfterReader{r: strings.NewReader("partial"), err: errors.New("context deadline exceeded")})},
			wantData:    "partial",
			wantErr:     true,
			errContains: []string{"deadline"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := NewProxyFunc(func(opts models.DockerProxyRequestOptions) (*http.Response, error) {
				return tt.resp, tt.doErr
			})
			data, err := fn(models.DockerProxyRequestOptions{Method: "GET", Path: "/containers/abc/json"})
			assert.Equal(t, tt.wantData, string(data))
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			for _, s := range tt.errContains {
				assert.Contains(t, err.Error(), s)
			}
			if tt.errExcludes != "" {
				assert.NotContains(t, err.Error(), tt.errExcludes)
			}
		})
	}
}
