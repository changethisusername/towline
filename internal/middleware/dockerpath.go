package middleware

import (
	"fmt"
	"regexp"
	"strings"
)

// dockerVersionPrefix matches an API version prefix such as /v1.43, which
// Docker accepts in front of every endpoint.
var dockerVersionPrefix = regexp.MustCompile(`^/v[0-9]+(\.[0-9]+)*(/|$)`)

// NormalizeDockerPath validates a Docker API path supplied by the agent and
// returns it with any API version prefix removed, so that scoping decisions
// are made on the same route Docker will actually serve.
//
// Paths containing query strings, fragments, percent-encoding, backslashes,
// dot segments, empty segments, or non-printable characters are rejected:
// the Portainer client concatenates the path into the request URL, so any of
// these could make the route Docker sees differ from the one checked here.
func NormalizeDockerPath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("docker API path must start with '/'")
	}
	for _, r := range p {
		if r <= 0x20 || r >= 0x7f {
			return "", fmt.Errorf("docker API path contains an invalid character")
		}
	}
	if strings.ContainsAny(p, "?#%\\") {
		return "", fmt.Errorf("docker API path must not contain '?', '#', '%%' or '\\'; pass query parameters via queryParams")
	}
	if strings.Contains(p, "//") {
		return "", fmt.Errorf("docker API path must not contain empty segments")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("docker API path must not contain '.' or '..' segments")
		}
	}

	if loc := dockerVersionPrefix.FindStringIndex(p); loc != nil {
		p = "/" + p[loc[1]:]
	}
	return p, nil
}

// isReadMethod reports whether an HTTP method has no side effects.
func isReadMethod(method string) bool {
	return method == "GET" || method == "HEAD"
}

// containerActionRoute matches POST /containers/{id}/{action}.
var containerActionRoute = regexp.MustCompile(`^/containers/([^/]+)/([a-z]+)$`)

// containerRoute matches /containers/{id}.
var containerRoute = regexp.MustCompile(`^/containers/([^/]+)$`)

// allowedContainerActions are the POST actions permitted on a stack's own
// containers. Anything else that mutates state (container create, exec,
// archive upload, networks, volumes, images, system, ...) is rejected.
var allowedContainerActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
	"kill":    true,
	"pause":   true,
	"unpause": true,
	"wait":    true,
	"resize":  true,
	"rename":  true,
	"update":  true,
}

// operationalContainerActions are lifecycle actions classified as
// Operational (no approval in prod).
var operationalContainerActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
}

// isAllowedMutation reports whether a non-read request to a normalized path
// is one of the container operations the scoped agent may perform.
func isAllowedMutation(path, method string) bool {
	switch method {
	case "POST":
		m := containerActionRoute.FindStringSubmatch(path)
		return m != nil && !reservedContainerIDs[m[1]] && allowedContainerActions[m[2]]
	case "DELETE":
		m := containerRoute.FindStringSubmatch(path)
		return m != nil && !reservedContainerIDs[m[1]]
	}
	return false
}

// isOperationalDockerPath returns true if the request is a container
// lifecycle action (restart, start, stop) that is classified as Operational.
// The path must already be normalized.
func isOperationalDockerPath(path, method string) bool {
	if method != "POST" {
		return false
	}
	m := containerActionRoute.FindStringSubmatch(path)
	return m != nil && !reservedContainerIDs[m[1]] && operationalContainerActions[m[2]]
}

// reservedContainerIDs are /containers/{x} segments that are collection
// endpoints rather than container identifiers.
var reservedContainerIDs = map[string]bool{
	"json":   true,
	"create": true,
	"prune":  true,
}
