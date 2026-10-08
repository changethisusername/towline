package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
)

// ProxyDockerRequest proxies a Docker API request to a specific Portainer environment.
//
// The request is built here rather than through the SDK proxy, which
// hardcodes an https:// scheme and so cannot reach a Portainer served over
// plain http. It uses the same server URL, API key and TLS settings as the
// other raw API calls, but no client-wide timeout: Docker calls such as
// image pulls or attached exec can legitimately run long, so callers bound
// them with opts.Context instead.
//
// Parameters:
//   - opts: Options defining the proxied request (environmentID, method, path, query params, headers, body, context)
//
// Returns:
//   - *http.Response: The response from the Docker API
//   - error: Any error that occurred during the request
func (c *PortainerClient) ProxyDockerRequest(opts models.DockerProxyRequestOptions) (*http.Response, error) {
	if c.rawCli == nil {
		return nil, fmt.Errorf("failed to proxy docker request: client not initialized")
	}

	u, err := url.Parse(c.rawCli.serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server URL: %w", err)
	}
	apiPath := opts.Path
	if !strings.HasPrefix(apiPath, "/") {
		apiPath = "/" + apiPath
	}
	// Setting Path (rather than concatenating a URL string) means characters
	// such as '?' or '#' in opts.Path are escaped instead of changing the URL.
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/api/endpoints/%d/docker", opts.EnvironmentID) + apiPath
	u.RawPath = ""
	u.RawQuery = ""
	if len(opts.QueryParams) > 0 {
		q := url.Values{}
		for k, v := range opts.QueryParams {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, opts.Method, u.String(), opts.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxy request: %w", err)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	// Set last so a caller-supplied header cannot replace the scoped key.
	req.Header.Set("X-API-Key", c.rawCli.token)

	resp, err := c.rawCli.proxyHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send proxy request: %w", err)
	}
	return resp, nil
}

// proxyHTTPClient returns an HTTP client with the same transport (and so the
// same TLS settings) as the API client, but without its overall timeout.
func (c *rawHTTPClient) proxyHTTPClient() *http.Client {
	if c.httpCli == nil {
		return http.DefaultClient
	}
	return &http.Client{
		Transport:     c.httpCli.Transport,
		CheckRedirect: c.httpCli.CheckRedirect,
		Jar:           c.httpCli.Jar,
	}
}
