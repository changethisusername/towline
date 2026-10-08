package gateway

// Regression tests for findings from the pre-release security review.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
)

// streamingPortainer is mockPortainer plus a Docker /events endpoint that
// streams forever, like the real one.
type streamingPortainer struct {
	*mockPortainer
	active atomic.Int64
	stop   chan struct{}
}

func (s *streamingPortainer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/api/endpoints/3/docker/events" && r.Header.Get("X-API-Key") == "tok-alpha" {
		s.active.Add(1)
		defer s.active.Add(-1)
		fl, _ := w.(http.Flusher)
		for {
			if _, err := io.WriteString(w, `{"status":"tick"}`+"\n"); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-s.stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	s.mockPortainer.ServeHTTP(w, r)
}

func newStreamingE2EGateway(t *testing.T) (*testGateway, *streamingPortainer) {
	t.Helper()
	sp := &streamingPortainer{
		mockPortainer: &mockPortainer{files: map[int]string{1: "services:\n  web:\n    image: nginx\n", 2: "services:\n  api:\n    image: api:1\n"}},
		stop:          make(chan struct{}),
	}
	portainer := httptest.NewServer(sp)
	srv := httptest.NewUnstartedServer(nil)
	public := "http://" + srv.Listener.Addr().String()
	cfg := &Config{
		PublicURL:      public,
		DataDir:        t.TempDir(),
		OwnerTokenHash: approval.HashToken(testOwnerToken),
		Portainer:      PortainerConfig{URL: portainer.URL},
		Projects: []ProjectConfig{
			{Name: "alpha-dev", Tier: "dev", Token: "tok-alpha", Approval: ApprovalProd},
			{Name: "beta-prod", Tier: "prod", Token: "tok-beta"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	audit := zerolog.Nop()
	g, err := New(cfg, Options{Audit: &audit})
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = g.Handler()
	srv.Start()
	t.Cleanup(func() {
		close(sp.stop)
		portainer.CloseClientConnections()
		portainer.Close()
		srv.Close()
		g.Close()
	})
	return &testGateway{Gateway: g, srv: srv, url: public}, sp
}

// FINDING: a READ-scoped connection can start Docker proxy GETs that never
// end (/events, /containers/{id}/stats, logs?follow=1). runDetached keeps
// them running with no deadline after the client is gone and after the
// connection is revoked; each one holds a goroutine, a Portainer connection
// and an ever-growing io.ReadAll buffer.
func TestStreamingDockerReadsAreRefused(t *testing.T) {
	tg, sp := newStreamingE2EGateway(t)
	tg.mcp.slowCallAfter = 200 * time.Millisecond
	read := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha-dev")
	c := mcpClient(t, tg.url+"/p/alpha-dev/mcp", read.Secret)

	const n = 5
	for i := 0; i < n; i++ {
		out, _ := callArgs(t, c, "dockerProxy", map[string]any{"environmentId": 3, "method": "GET", "dockerAPIPath": "/events"})
		t.Logf("call %d: %.80s", i, out)
	}
	if err := tg.store.RevokeConnection(read.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && sp.active.Load() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if got := sp.active.Load(); got != 0 {
		t.Fatalf("%d Docker streams started by a revoked read-only connection are still running", got)
	}
}

// FINDING: refresh-token reuse detection only works during the 2-minute
// grace window. A thief who redeems a stolen refresh token first keeps the
// rotated chain forever: when the legitimate client later presents the old
// token it gets a plain invalid_grant and the grant is NOT revoked.
func TestRefreshReuseAfterGraceRevokesGrant(t *testing.T) {
	s, _ := OpenStore("")
	var mu sync.Mutex
	clock := time.Now()
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	known := func(string) bool { return true }
	if _, err := s.OpenPairing("p", []string{"alpha"}, middleware.ScopeDeploy, known); err != nil {
		t.Fatal(err)
	}
	cb := "https://claude.ai/api/mcp/auth_callback"
	clientID, _, err := s.registerClient("app", []string{cb}, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.approveClient(clientID, s.Pairings()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	verifier, challenge := pkce()
	code, _ := s.issueCode(conn, cb, true, "https://x/mcp", challenge)
	legit, _, err := s.redeemCode(conn, code, cb, verifier, "")
	if err != nil {
		t.Fatal(err)
	}

	// Attacker uses the stolen refresh token first.
	thief, _, err := s.refreshAccess(conn, legit.Refresh, "")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	clock = clock.Add(5 * time.Minute)
	mu.Unlock()
	// The legitimate client refreshes later with the (now rotated) token.
	if _, _, err := s.refreshAccess(conn, legit.Refresh, ""); err == nil {
		t.Fatal("rotated token accepted after grace")
	}
	// Reuse of a rotated token must revoke the whole grant.
	if _, _, err := s.refreshAccess(conn, thief.Refresh, ""); err == nil {
		t.Fatal("stolen refresh chain survived reuse of the rotated token: grant was not revoked")
	}
}

// FINDING: the consent page is rendered to unauthenticated visitors and
// lists every open pairing (connection name, project names, scope, id).
// During a pairing anyone can register a client and GET /oauth/authorize,
// so this reveals project names the metadata endpoint deliberately hides.
func TestConsentPageHidesPairingsFromAnonymous(t *testing.T) {
	tg := newTestGateway(t)
	known := func(n string) bool { _, ok := tg.cfg.Project(n); return ok }
	p, err := tg.store.OpenPairing("dom-private-laptop", []string{"beta"}, middleware.ScopeDeploy, known)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"redirect_uris":["https://attacker.example/cb"],"client_name":"x","token_endpoint_auth_method":"none"}`
	resp, err := http.Post(tg.url+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	_, challenge := pkce()
	resp, err = http.Get(authorizeURL(tg.url, m["client_id"].(string), "https://attacker.example/cb", challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(page), p.Name) || strings.Contains(string(page), p.ID) || strings.Contains(string(page), "deploy on beta") {
		t.Fatal("anonymous consent page shows open pairings (names, projects, scope, ids)")
	}
}

// FINDING: pending authorization requests share one global cap of 100.
// Anyone who knows an approved public app's client id (not a secret) can
// fill it and block every owner sign-in for 10 minutes, repeatedly.
func TestAuthorizeFloodDoesNotBlockOwnerSignIn(t *testing.T) {
	tg := newTestGateway(t)
	tg.cfg.TrustCloudflare = true // as deployed behind cloudflared
	known := func(n string) bool { _, ok := tg.cfg.Project(n); return ok }
	cb := "https://claude.ai/api/mcp/auth_callback"
	if _, err := tg.store.OpenPairing("p", []string{"alpha"}, middleware.ScopeRead, known); err != nil {
		t.Fatal(err)
	}
	victimApp, _, _ := tg.store.registerClient("Claude", []string{cb}, false)
	if _, err := tg.store.approveClient(victimApp, tg.store.Pairings()[0].ID); err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce()
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequest("GET", authorizeURL(tg.url, victimApp, cb, challenge, ""), nil)
		req.Header.Set("CF-Connecting-IP", "198.51.100."+itoa(i/50+1))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	resp, err := http.Get(authorizeURL(tg.url, victimApp, cb, challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(page), "Too many sign-ins") {
		t.Fatal("anonymous requests from two addresses blocked the owner's sign-in")
	}
}

// FINDING: runDetached runs tool handlers in a goroutine that mcp-go's
// WithRecovery doesn't cover, so a panic in any tool handler (reachable by
// any connection) kills the whole gateway instead of failing one call.
func TestPanicInToolDoesNotCrashGateway(t *testing.T) {
	if os.Getenv("SECREVIEW_PANIC_CHILD") == "1" {
		panicBuilder := func(g *Gateway, p ProjectConfig) (*BuiltProject, error) {
			h := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var m map[string]int
				m["boom"] = 1 // nil map write: a typical handler bug
				return nil, nil
			}
			return &BuiltProject{Tools: map[string]server.ServerTool{
				"towline_service_health": {Tool: mcp.NewTool("towline_service_health"), Handler: middleware.NewCallerGate("towline_service_health", true)(h)},
			}}, nil
		}
		srv := httptest.NewUnstartedServer(nil)
		public := "http://" + srv.Listener.Addr().String()
		cfg := &Config{PublicURL: public, DataDir: t.TempDir(), OwnerTokenHash: approval.HashToken(testOwnerToken),
			Portainer: PortainerConfig{URL: "https://portainer.invalid"},
			Projects:  []ProjectConfig{{Name: "alpha", Stack: "alpha-dev", Tier: "dev", Token: "x"}}}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		audit := zerolog.Nop()
		g, err := New(cfg, Options{Builder: panicBuilder, Audit: &audit})
		if err != nil {
			t.Fatal(err)
		}
		srv.Config.Handler = g.Handler()
		srv.Start()
		tg := &testGateway{Gateway: g, srv: srv, url: public}
		read := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha")
		c := mcpClient(t, public+"/mcp", read.Secret)
		callTool(t, c, "alpha__towline_service_health")
		time.Sleep(200 * time.Millisecond)
		return // survived
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPanicInToolDoesNotCrashGateway$")
	cmd.Env = append(os.Environ(), "SECREVIEW_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gateway process died from a panic in a tool handler: %v\n%s", err, firstLines(string(out), 6))
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

var _ = url.Values{}
