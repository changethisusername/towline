package towline

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
)

// maxProxyErrorBody caps how much of an error response body is quoted in
// the error returned by a ProxyFunc.
const maxProxyErrorBody = 512

// NewProxyFunc adapts a raw Docker proxy call into a ProxyFunc.
//
// A response with status >= 400 is returned as an error that quotes the
// (truncated) body, so Docker or Portainer error JSON is never mistaken for
// data by callers such as exec or the ownership check. If reading the body
// fails part way (for example because the request context expired), the
// bytes read so far are returned along with the error.
func NewProxyFunc(do func(opts models.DockerProxyRequestOptions) (*http.Response, error)) ProxyFunc {
	return func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		resp, err := do(opts)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= http.StatusBadRequest {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProxyErrorBody+1))
			return nil, fmt.Errorf("docker API %s %s returned HTTP %d: %s",
				opts.Method, opts.Path, resp.StatusCode, proxyErrorMessage(body))
		}

		return io.ReadAll(resp.Body)
	}
}

// proxyErrorMessage extracts Docker's {"message": "..."} if present,
// otherwise returns the body itself, truncated to maxProxyErrorBody bytes.
func proxyErrorMessage(body []byte) string {
	var parsed struct {
		Message string `json:"message"`
	}
	msg := string(body)
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Message != "" {
		msg = parsed.Message
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > maxProxyErrorBody {
		msg = msg[:maxProxyErrorBody] + "...(truncated)"
	}
	if msg == "" {
		msg = "(empty body)"
	}
	return msg
}
