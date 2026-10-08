package gateway

import (
	"strings"
	"testing"

	"github.com/changethisusername/towline/internal/middleware"
)

// Checks that passed (kept as regression tests).
func TestSecurityReadScopeDockerMethodTricks(t *testing.T) {
	tg, mp := newE2EGateway(t)
	read := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha-dev")
	c := mcpClient(t, tg.url+"/mcp", read.Secret)
	startN := mp.count("tok-beta GET /api/stacks/2/file")
	for _, m := range []string{"post", "Post", "OPTIONS", "PUT", "DELETE", "PATCH", "get ", "CONNECT"} {
		out, isErr := callArgs(t, c, "alpha-dev__dockerProxy", map[string]any{"environmentId": 3, "method": m, "dockerAPIPath": "/containers/x/start"})
		if !isErr {
			t.Fatalf("method %q allowed: %s", m, out)
		}
	}
	for _, name := range []string{"alpha-dev__updateLocalStack", "beta-prod__getLocalStackFile", "alpha-dev____guide", "towline_guide__x", "alpha-dev__towline_exec"} {
		if _, isErr := callArgs(t, c, name, map[string]any{"id": 2, "environmentId": 3, "file": "services: {}"}); !isErr {
			t.Fatalf("%s allowed for read alpha-dev", name)
		}
	}
	if mp.count("tok-beta GET /api/stacks/2/file") != startN {
		t.Fatal("beta stack read through an alpha-only connection")
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	for _, r := range mp.requests {
		if strings.HasPrefix(r, "tok-alpha") && !strings.Contains(r, " GET ") {
			t.Fatalf("write reached Portainer: %s", r)
		}
	}
}

func TestSecurityTokenKindsAndEndpoints(t *testing.T) {
	tg := newTestGateway(t)
	only := tg.createConn(t, KindToken, middleware.ScopeDeploy, "alpha")
	if s := rawStatus(t, tg.url+"/p/beta/mcp", only.Secret, nil); s != 404 {
		t.Fatalf("other project endpoint: %d", s)
	}
	// access token bound to /p/alpha can't be used on /mcp or /p/beta
	tg.store.mu.Lock()
	pair, _ := tg.store.issueTokensLocked(only.ID, tg.resourceFor("alpha"), "", true)
	tg.store.mu.Unlock()
	if s := rawStatus(t, tg.url+"/mcp", pair.Access, nil); s != 401 {
		t.Fatalf("project token on /mcp: %d", s)
	}
	if s := rawStatus(t, tg.url+"/mcp", pair.Refresh, nil); s != 401 {
		t.Fatalf("refresh as bearer: %d", s)
	}
	if s := rawStatus(t, tg.url+"/p/alpha/mcp", pair.Access, nil); s != 200 {
		t.Fatalf("bound endpoint: %d", s)
	}
}
