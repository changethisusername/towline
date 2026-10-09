package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
)

func TestNoServerSessionsKept(t *testing.T) {
	tg := newTestGateway(t)
	tok := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha")
	req, _ := http.NewRequest("POST", tg.url+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.Secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize: %d session=%q", resp.StatusCode, resp.Header.Get("Mcp-Session-Id"))
	}
	get, _ := http.NewRequest("GET", tg.url+"/mcp", nil)
	get.Header.Set("Authorization", "Bearer "+tok.Secret)
	resp, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET stream: %d, want 405", resp.StatusCode)
	}
}

func TestBadTokensDontLockOutValidOnes(t *testing.T) {
	tg := newTestGateway(t)
	tok := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha")
	for i := 0; i < limitAuthFailPerIP+5; i++ {
		rawStatus(t, tg.url+"/mcp", "twl_ct_bad", nil)
	}
	if s := rawStatus(t, tg.url+"/mcp", "twl_ct_bad", nil); s != http.StatusTooManyRequests {
		t.Fatalf("bad tokens over the limit: %d", s)
	}
	if s := rawStatus(t, tg.url+"/mcp", tok.Secret, nil); s != http.StatusOK {
		t.Fatalf("valid token from the same address: %d", s)
	}
}

func TestOwnerLoginHasNoGlobalLockout(t *testing.T) {
	tg := newTestGateway(t)
	for i := 0; i < 200; i++ {
		tg.ui.loginFailed("198.51.100." + itoa(i%250))
	}
	if !tg.ui.loginAllowed("203.0.113.9") {
		t.Fatal("failures from other addresses locked out the owner")
	}
}

func TestRemovedProjectLosesConnections(t *testing.T) {
	tg := newTestGateway(t)
	both := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha", "beta")
	onlyBeta := tg.createConn(t, KindToken, middleware.ScopeRead, "beta")

	// Restart without beta.
	cfg := *tg.cfg
	cfg.Projects = cfg.Projects[:1]
	g2, err := New(&cfg, Options{Builder: tg.fake.build})
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()
	if c := g2.store.Authenticate(both.Secret, ""); c == nil || strings.Join(c.Projects, ",") != "alpha" {
		t.Fatalf("connection after removal: %+v", c)
	}
	if g2.store.Authenticate(onlyBeta.Secret, "") != nil {
		t.Fatal("connection with only a removed project survived")
	}
	// Adding beta back grants nothing old.
	g3, err := New(tg.cfg, Options{Builder: tg.fake.build})
	if err != nil {
		t.Fatal(err)
	}
	defer g3.Close()
	if c := g3.store.Authenticate(both.Secret, ""); c == nil || c.HasProject("beta") {
		t.Fatalf("re-added project restored old access: %+v", c)
	}
}

func TestRedirectURIOptionalAtTokenWhenOmitted(t *testing.T) {
	tg := newTestGateway(t)
	c, err := tg.store.CreateConnection(NewConnectionRequest{Name: "x", Kind: KindOAuth, Projects: []string{"alpha"}, Scope: middleware.ScopeRead}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// Leave only one registered redirect URI so it can be omitted.
	tg.store.mu.Lock()
	tg.store.conns[c.ID].RedirectURIs = DefaultRedirectURIs[:1]
	tg.store.mu.Unlock()
	hc := noRedirectClient()
	verifier, challenge := pkce()
	u := strings.Replace(authorizeURL(tg.url, c.ClientID, "", challenge, ""), "redirect_uri=&", "", 1)
	resp, err := hc.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("authorize without redirect_uri: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}}
	if resp, m := postForm(t, hc, tg.url+"/oauth/token", form, c.ClientID, c.Secret); resp.StatusCode != http.StatusOK {
		t.Fatalf("token without redirect_uri: %d %v", resp.StatusCode, m)
	}
}

func TestRotatedRefreshTokenReuseRevokesGrant(t *testing.T) {
	s, _ := OpenStore("")
	known := func(string) bool { return true }
	if _, err := s.OpenPairing("p", []string{"alpha"}, middleware.ScopeRead, known); err != nil {
		t.Fatal(err)
	}
	clientID, _, err := s.registerClient("app", []string{"https://claude.ai/api/mcp/auth_callback"}, false)
	if err != nil {
		t.Fatal(err)
	}
	pairs := s.Pairings()
	conn, err := s.approveClient(clientID, pairs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	verifier, challenge := pkce()
	code, _ := s.issueCode(conn, "https://claude.ai/api/mcp/auth_callback", true, "https://x/mcp", challenge)
	first, _, err := s.redeemCode(conn, code, "https://claude.ai/api/mcp/auth_callback", verifier, "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.refreshAccess(conn, first.Refresh, "")
	if err != nil {
		t.Fatal(err)
	}
	// A retry with the old token before the new one is used still works.
	if _, _, err := s.refreshAccess(conn, first.Refresh, ""); err != nil {
		t.Fatalf("retry within grace: %v", err)
	}
	third, _, err := s.refreshAccess(conn, second.Refresh, "")
	if err != nil {
		t.Fatal(err)
	}
	// The original token turns up again after its successor was used:
	// someone copied it. Everything from this grant is revoked.
	if _, _, err := s.refreshAccess(conn, first.Refresh, ""); err == nil {
		t.Fatal("stale refresh token accepted")
	}
	if _, _, err := s.refreshAccess(conn, third.Refresh, ""); err == nil {
		t.Fatal("grant not revoked after reuse")
	}
	if s.Authenticate(third.Access, "https://x/mcp") != nil {
		t.Fatal("access token survived grant revocation")
	}
}

func TestPendingApprovalsCappedPerConnection(t *testing.T) {
	tg := newTestGateway(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := tg.approvals.Submit(ctx, approval.Request{ID: "a" + itoa(i), Project: "beta", Action: "x", Client: "conn_a"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tg.approvals.Submit(ctx, approval.Request{ID: "a-over", Project: "beta", Action: "x", Client: "conn_a"}); err == nil {
		t.Fatal("11th pending request from one connection accepted")
	}
	if err := tg.approvals.Submit(ctx, approval.Request{ID: "b1", Project: "beta", Action: "x", Client: "conn_b"}); err != nil {
		t.Fatalf("another connection was crowded out: %v", err)
	}
}
