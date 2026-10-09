package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
)

const testOwnerToken = "owner-token-for-tests-0123456789abcdef0123456789abcdef"

// fakeBuilder gives every project a read tool and a deploy tool, wrapped in
// the real caller gate, and records calls.
type fakeBuilder struct {
	calls []string
}

func (f *fakeBuilder) build(g *Gateway, p ProjectConfig) (*BuiltProject, error) {
	mk := func(name string) server.ServerTool {
		h := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			c, _ := middleware.CallerFromContext(ctx)
			f.calls = append(f.calls, p.Name+"/"+name+"/"+c.ID)
			return mcp.NewToolResultText("ran " + p.Name + "/" + name), nil
		}
		return server.ServerTool{
			Tool:    mcp.NewTool(name, mcp.WithDescription(name)),
			Handler: middleware.NewCallerGate(name, true)(h),
		}
	}
	// Upstream marks dockerProxy read-only although it can write.
	proxy := mk("dockerProxy")
	proxy.Tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(true)
	health := mk("towline_service_health")
	health.Tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(true)
	return &BuiltProject{Tools: map[string]server.ServerTool{
		"towline_service_health": health,
		"updateLocalStack":       mk("updateLocalStack"),
		"dockerProxy":            proxy,
	}}, nil
}

type testGateway struct {
	*Gateway
	srv     *httptest.Server
	url     string
	fake    *fakeBuilder
	auditFn func() string
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	public := "http://" + srv.Listener.Addr().String()
	cfg := &Config{
		PublicURL:      public,
		DataDir:        t.TempDir(),
		OwnerTokenHash: approval.HashToken(testOwnerToken),
		Portainer:      PortainerConfig{URL: "https://portainer.invalid"},
		Projects: []ProjectConfig{
			{Name: "alpha", Stack: "alpha-dev", Tier: "dev", Token: "ptr_a"},
			{Name: "beta", Stack: "beta-prod", Tier: "prod", Token: "ptr_b"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var auditBuf strings.Builder
	audit := zerolog.New(&auditBuf)
	fb := &fakeBuilder{}
	g, err := New(cfg, Options{Builder: fb.build, Audit: &audit})
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = g.Handler()
	srv.Start()
	t.Cleanup(func() { srv.Close(); g.Close() })
	return &testGateway{Gateway: g, srv: srv, url: public, fake: fb, auditFn: auditBuf.String}
}

func (tg *testGateway) createConn(t *testing.T, kind string, scope middleware.Scope, projects ...string) *CreatedConnection {
	t.Helper()
	c, err := tg.store.CreateConnection(NewConnectionRequest{Name: "test " + kind, Kind: kind, Projects: projects, Scope: scope},
		func(n string) bool { _, ok := tg.cfg.Project(n); return ok })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mcpClient(t *testing.T, endpoint, token string) *client.Client {
	t.Helper()
	c, err := client.NewStreamableHttpClient(endpoint, transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func toolNames(t *testing.T, c *client.Client) []string {
	t.Helper()
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func callTool(t *testing.T, c *client.Client, name string) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = map[string]any{}
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		return err.Error(), true
	}
	var text string
	for _, ct := range res.Content {
		if tc, ok := ct.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, res.IsError
}

func TestUnauthenticatedGets401WithMetadata(t *testing.T) {
	tg := newTestGateway(t)
	for _, path := range []string{"/mcp", "/p/alpha/mcp", "/p/nope/mcp"} {
		resp, err := http.Post(tg.url+path, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", path, resp.StatusCode)
		}
		want := `resource_metadata="` + tg.url + "/.well-known/oauth-protected-resource" + path + `"`
		if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, want) {
			t.Fatalf("%s: WWW-Authenticate %q, want %q", path, got, want)
		}
	}
}

func TestTokenConnectionScoping(t *testing.T) {
	tg := newTestGateway(t)
	deployAlpha := tg.createConn(t, KindToken, middleware.ScopeDeploy, "alpha")
	readBoth := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha", "beta")

	c := mcpClient(t, tg.url+"/mcp", deployAlpha.Secret)
	names := strings.Join(toolNames(t, c), ",")
	if !strings.Contains(names, "alpha__updateLocalStack") || strings.Contains(names, "beta__") {
		t.Fatalf("deploy alpha tools = %s", names)
	}
	if out, isErr := callTool(t, c, "alpha__updateLocalStack"); isErr || out != "ran alpha/updateLocalStack" {
		t.Fatalf("alpha deploy call: %q %v", out, isErr)
	}
	// Another project's tool is refused even though it exists.
	if out, isErr := callTool(t, c, "beta__towline_service_health"); !isErr || !strings.Contains(out, "unknown tool") {
		t.Fatalf("cross-project call: %q %v", out, isErr)
	}

	r := mcpClient(t, tg.url+"/mcp", readBoth.Secret)
	names = strings.Join(toolNames(t, r), ",")
	if strings.Contains(names, "updateLocalStack") || !strings.Contains(names, "beta__towline_service_health") || !strings.Contains(names, guideTool) {
		t.Fatalf("read tools = %s", names)
	}
	if out, isErr := callTool(t, r, "alpha__updateLocalStack"); !isErr || !strings.Contains(out, "read access") {
		t.Fatalf("read scope deploy call: %q %v", out, isErr)
	}
	for _, call := range tg.fake.calls {
		if strings.Contains(call, "beta") || strings.HasSuffix(call, readBoth.ID) && strings.Contains(call, "updateLocalStack") {
			t.Fatalf("forbidden call reached a handler: %s", call)
		}
	}

	// Per-project endpoint: plain names, and only the connection's projects.
	p := mcpClient(t, tg.url+"/p/alpha/mcp", deployAlpha.Secret)
	if out, isErr := callTool(t, p, "updateLocalStack"); isErr || out != "ran alpha/updateLocalStack" {
		t.Fatalf("per-project call: %q %v", out, isErr)
	}
	req, _ := http.NewRequest("POST", tg.url+"/p/beta/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+deployAlpha.Secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other project endpoint: %d, want 404", resp.StatusCode)
	}

	// Revocation applies to the next request.
	if err := tg.store.RevokeConnection(deployAlpha.ID); err != nil {
		t.Fatal(err)
	}
	if status := rawStatus(t, tg.url+"/mcp", deployAlpha.Secret, nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", status)
	}
	if !strings.Contains(tg.auditFn(), `"tool":"updateLocalStack"`) {
		t.Fatal("tool call missing from audit log")
	}
}

func rawStatus(t *testing.T, endpoint, token string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestHostOriginAndCredentialKinds(t *testing.T) {
	tg := newTestGateway(t)
	tok := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha")
	oc := tg.createConn(t, KindOAuth, middleware.ScopeRead, "alpha")

	if s := rawStatus(t, tg.url+"/mcp", tok.Secret, map[string]string{"Host": "evil.example"}); s != http.StatusMisdirectedRequest {
		t.Fatalf("wrong host: %d", s)
	}
	if s := rawStatus(t, tg.url+"/mcp", tok.Secret, map[string]string{"Origin": "https://evil.example"}); s != http.StatusForbidden {
		t.Fatalf("browser origin: %d", s)
	}
	// A client secret is not a bearer token.
	if s := rawStatus(t, tg.url+"/mcp", oc.Secret, nil); s != http.StatusUnauthorized {
		t.Fatalf("client secret as bearer: %d", s)
	}
	if s := rawStatus(t, tg.url+"/mcp", "twl_ct_"+strings.Repeat("0", 64), nil); s != http.StatusUnauthorized {
		t.Fatalf("made-up token: %d", s)
	}
}

func pkce() (verifier, challenge string) {
	verifier = randomHex(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func noRedirectClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func authorizeURL(base, clientID, redirect, challenge, resource string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st8"},
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	return base + "/oauth/authorize?" + q.Encode()
}

func postForm(t *testing.T, c *http.Client, endpoint string, form url.Values, basicID, basicSecret string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicID != "" {
		req.SetBasicAuth(url.QueryEscape(basicID), url.QueryEscape(basicSecret))
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return resp, m
}

func TestPreRegisteredOAuthFlow(t *testing.T) {
	tg := newTestGateway(t)
	oc := tg.createConn(t, KindOAuth, middleware.ScopeDeploy, "alpha")
	hc := noRedirectClient()
	redirect := DefaultRedirectURIs[0]
	verifier, challenge := pkce()

	// Bad client or redirect: an error page, never a redirect.
	for _, u := range []string{
		authorizeURL(tg.url, "twl_ci_unknown", redirect, challenge, ""),
		authorizeURL(tg.url, oc.ClientID, "https://evil.example/cb", challenge, ""),
	} {
		resp, err := hc.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
			t.Fatalf("bad authorize %s: %d %q", u, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// No PKCE: error back to the client.
	resp, err := hc.Get(strings.Replace(authorizeURL(tg.url, oc.ClientID, redirect, challenge, ""), "code_challenge_method=S256", "code_challenge_method=plain", 1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, redirect) || !strings.Contains(loc, "error=invalid_request") {
		t.Fatalf("plain PKCE: %q", loc)
	}

	// Confidential client: code issued straight away.
	resp, err = hc.Get(authorizeURL(tg.url, oc.ClientID, redirect, challenge, tg.url+"/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" || loc.Query().Get("state") != "st8" || loc.Query().Get("iss") != tg.url {
		t.Fatalf("authorize redirect %q", loc)
	}

	tokenURL := tg.url + "/oauth/token"
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	// Without the secret: refused.
	if resp, _ := postForm(t, hc, tokenURL, form, oc.ClientID, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no secret: %d", resp.StatusCode)
	}
	// Wrong verifier: refused (and the code is not burned by it).
	bad := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {strings.Repeat("a", 43)}}
	if resp, m := postForm(t, hc, tokenURL, bad, oc.ClientID, oc.Secret); resp.StatusCode != http.StatusBadRequest || m["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %d %v", resp.StatusCode, m)
	}
	resp, tok := postForm(t, hc, tokenURL, form, oc.ClientID, oc.Secret)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token: %d %v", resp.StatusCode, tok)
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if !strings.HasPrefix(access, prefixAccessToken) || !strings.HasPrefix(refresh, prefixRefreshToken) {
		t.Fatalf("token response %v", tok)
	}
	c := mcpClient(t, tg.url+"/mcp", access)
	if out, isErr := callTool(t, c, "alpha__updateLocalStack"); isErr {
		t.Fatalf("call with access token: %s", out)
	}
	// A token for /mcp also works on the per-project endpoints of its own
	// projects (clients that don't send 'resource' get one), not others.
	if s := rawStatus(t, tg.url+"/p/alpha/mcp", access, nil); s != http.StatusOK {
		t.Fatalf("/mcp token on its project's endpoint: %d", s)
	}
	if s := rawStatus(t, tg.url+"/p/beta/mcp", access, nil); s != http.StatusNotFound {
		t.Fatalf("/mcp token on another project's endpoint: %d", s)
	}
	// Refresh tokens are not bearer tokens.
	if s := rawStatus(t, tg.url+"/mcp", refresh, nil); s != http.StatusUnauthorized {
		t.Fatalf("refresh token as bearer: %d", s)
	}

	// Replaying the code fails and revokes what it issued.
	if resp, _ := postForm(t, hc, tokenURL, form, oc.ClientID, oc.Secret); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("code replay: %d", resp.StatusCode)
	}
	if s := rawStatus(t, tg.url+"/mcp", access, nil); s != http.StatusUnauthorized {
		t.Fatalf("access token after code replay: %d", s)
	}
}

func TestRefreshAndRevoke(t *testing.T) {
	tg := newTestGateway(t)
	oc := tg.createConn(t, KindOAuth, middleware.ScopeRead, "alpha")
	hc := noRedirectClient()
	verifier, challenge := pkce()
	resp, _ := hc.Get(authorizeURL(tg.url, oc.ClientID, DefaultRedirectURIs[1], challenge, tg.url+"/p/alpha/mcp"))
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	form := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "redirect_uri": {DefaultRedirectURIs[1]},
		"code_verifier": {verifier}, "client_id": {oc.ClientID}, "client_secret": {oc.Secret}, "resource": {tg.url + "/p/alpha/mcp"}}
	_, tok := postForm(t, hc, tg.url+"/oauth/token", form, "", "")
	refresh, _ := tok["refresh_token"].(string)
	if refresh == "" {
		t.Fatalf("no refresh token: %v", tok)
	}
	rf := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	resp2, tok2 := postForm(t, hc, tg.url+"/oauth/token", rf, oc.ClientID, oc.Secret)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("refresh: %d %v", resp2.StatusCode, tok2)
	}
	access := tok2["access_token"].(string)
	if s := rawStatus(t, tg.url+"/p/alpha/mcp", access, nil); s != http.StatusOK {
		t.Fatalf("refreshed token: %d", s)
	}
	// Another connection's secret can't use this refresh token.
	other := tg.createConn(t, KindOAuth, middleware.ScopeRead, "alpha")
	if resp, _ := postForm(t, hc, tg.url+"/oauth/token", rf, other.ClientID, other.Secret); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign refresh: %d", resp.StatusCode)
	}
	if resp, _ := postForm(t, hc, tg.url+"/oauth/revoke", url.Values{"token": {refresh}}, oc.ClientID, oc.Secret); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, _ := postForm(t, hc, tg.url+"/oauth/token", rf, oc.ClientID, oc.Secret); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("revoked refresh: %d", resp.StatusCode)
	}
}

var hiddenRequest = regexp.MustCompile(`name="request" value="([0-9a-f]+)"`)

func TestDynamicRegistrationNeedsPairingAndOwner(t *testing.T) {
	tg := newTestGateway(t)
	hc := noRedirectClient()
	reg := func() (*http.Response, map[string]any) {
		body := `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude","token_endpoint_auth_method":"none"}`
		resp, err := hc.Post(tg.url+"/oauth/register", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}
	if resp, _ := reg(); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("registration without pairing: %d", resp.StatusCode)
	}
	known := func(n string) bool { _, ok := tg.cfg.Project(n); return ok }
	pairing, err := tg.store.OpenPairing("my phone", []string{"beta"}, middleware.ScopeDeploy, known)
	if err != nil {
		t.Fatal(err)
	}
	resp, m := reg()
	if resp.StatusCode != http.StatusCreated || m["client_secret"] != nil {
		t.Fatalf("registration: %d %v", resp.StatusCode, m)
	}
	clientID := m["client_id"].(string)
	// A registered client can't get tokens before the owner approves it.
	verifier, challenge := pkce()
	resp, err = hc.Get(authorizeURL(tg.url, clientID, "https://claude.ai/api/mcp/auth_callback", challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Location") != "" || !strings.Contains(string(page), "Allow an app") {
		t.Fatalf("authorize for pending client: %d", resp.StatusCode)
	}
	// The details and the decision form are only shown to the owner.
	owner := noRedirectClient()
	if resp, _ := postForm(t, owner, tg.url+"/ui/login", url.Values{"token": {testOwnerToken}}, "", ""); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("owner login: %d", resp.StatusCode)
	}
	resp, err = owner.Get(authorizeURL(tg.url, clientID, "https://claude.ai/api/mcp/auth_callback", challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	reqID := hiddenRequest.FindStringSubmatch(string(page))[1]

	consent := func(form url.Values, origin string) *http.Response {
		req, _ := http.NewRequest("POST", tg.url+"/oauth/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	approve := url.Values{"request": {reqID}, "decision": {"approve"}, "pairing": {pairing.ID}}
	// Cross-site posts are refused.
	if resp := consent(approve, "https://evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site consent: %d", resp.StatusCode)
	}
	// Without the owner token: refused.
	if resp := consent(approve, tg.url); resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
		t.Fatalf("consent without owner: %d", resp.StatusCode)
	}
	approve.Set("owner_token", testOwnerToken)
	resp = consent(approve, tg.url)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("consent: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// The pairing is used up: no second app can register on it.
	if resp, _ := reg(); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("registration after pairing used: %d", resp.StatusCode)
	}

	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier}, "client_id": {clientID}}
	resp, tok := postForm(t, hc, tg.url+"/oauth/token", form, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public token: %d %v", resp.StatusCode, tok)
	}
	c := mcpClient(t, tg.url+"/mcp", tok["access_token"].(string))
	names := strings.Join(toolNames(t, c), ",")
	if !strings.Contains(names, "beta__updateLocalStack") || strings.Contains(names, "alpha__") {
		t.Fatalf("approved app tools: %s", names)
	}

	// Public clients rotate refresh tokens; the old one keeps working
	// briefly for retries.
	rf := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {clientID}}
	resp, tok2 := postForm(t, hc, tg.url+"/oauth/token", rf, "", "")
	if resp.StatusCode != http.StatusOK || tok2["refresh_token"] == nil || tok2["refresh_token"] == tok["refresh_token"] {
		t.Fatalf("public refresh: %d %v", resp.StatusCode, tok2)
	}
	// Re-authorizing a public client always needs the owner.
	resp, _ = hc.Get(authorizeURL(tg.url, clientID, "https://claude.ai/api/mcp/auth_callback", challenge, ""))
	resp.Body.Close()
	if resp.Header.Get("Location") != "" {
		t.Fatal("public client was authorized without consent")
	}
}

func TestOwnerUI(t *testing.T) {
	tg := newTestGateway(t)
	hc := noRedirectClient()
	post := func(path string, form url.Values) *http.Response {
		req, _ := http.NewRequest("POST", tg.url+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", tg.url)
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := post("/ui/login", url.Values{"token": {"wrong"}}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong login: %d", resp.StatusCode)
	}
	resp := post("/ui/login", url.Values{"token": {testOwnerToken}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	resp, err := hc.Get(tg.url + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(string(page))
	if csrf == nil {
		t.Fatal("no csrf token on dashboard")
	}
	create := url.Values{"name": {"laptop"}, "project": {"alpha"}, "scope": {"read"}, "method": {"token"}}
	if resp := post("/ui/connections", create); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create without csrf: %d", resp.StatusCode)
	}
	nonceRe := regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`)
	nonce := nonceRe.FindStringSubmatch(string(page))
	if nonce == nil {
		t.Fatal("no form nonce on dashboard")
	}
	create.Set("csrf", csrf[1])
	create.Set("nonce", nonce[1])
	resp = post("/ui/connections", create)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	secret := regexp.MustCompile(`twl_ct_[0-9a-f]{64}`).FindString(string(body))
	if resp.StatusCode != http.StatusOK || secret == "" {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if s := rawStatus(t, tg.url+"/mcp", secret, nil); s != http.StatusOK {
		t.Fatalf("created token: %d", s)
	}
	// Reloading the result page doesn't create a second connection.
	resp = post("/ui/connections", create)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || strings.Contains(string(body), "twl_ct_") {
		t.Fatalf("resubmitted form: %d", resp.StatusCode)
	}
	// Unknown projects are refused, and the form keeps what was entered.
	create.Set("nonce", nonceRe.FindStringSubmatch(string(body))[1])
	create.Set("project", "gamma")
	create.Set("name", "my laptop")
	resp = post("/ui/connections", create)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `value="my laptop"`) || !strings.Contains(string(body), `id="m-token" checked`) {
		t.Fatalf("unknown project: %d", resp.StatusCode)
	}
}

func TestApprovalFlowThroughUI(t *testing.T) {
	tg := newTestGateway(t)
	ctx := context.Background()
	if err := tg.approvals.Submit(ctx, approval.Request{ID: "req-1", Project: "beta", Action: "updateLocalStack", Description: "{}", Client: "conn_x"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := tg.approvals.Status(ctx, "req-1"); s != approval.StatusPending {
		t.Fatalf("status %s", s)
	}
	if err := tg.approvals.Decide("req-1", approval.StatusApproved); err != nil {
		t.Fatal(err)
	}
	if s, _ := tg.approvals.Status(ctx, "req-1"); s != approval.StatusApproved {
		t.Fatalf("status %s", s)
	}
	if err := tg.approvals.Decide("req-1", approval.StatusRejected); err == nil {
		t.Fatal("decided twice")
	}
}

func TestStatePersistsAndHashesSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConnection(NewConnectionRequest{Name: "x", Kind: KindToken, Projects: []string{"alpha"}, Scope: middleware.ScopeRead}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(mustOpen(t, dir+"/state.json"))
	if strings.Contains(string(raw), c.Secret) {
		t.Fatal("plaintext secret in state file")
	}
	s2, err := OpenStore(dir + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Authenticate(c.Secret, "") == nil {
		t.Fatal("connection lost on reload")
	}
	if err := s2.RevokeConnection(c.ID); err != nil {
		t.Fatal(err)
	}
	s3, _ := OpenStore(dir + "/state.json")
	if s3.Authenticate(c.Secret, "") != nil {
		t.Fatal("revocation lost on reload")
	}
}

func TestConfigValidation(t *testing.T) {
	base := func() Config {
		return Config{
			PublicURL:      "https://mcp.example.com",
			OwnerTokenHash: approval.HashToken("x"),
			Portainer:      PortainerConfig{URL: "https://p:9443"},
			Projects:       []ProjectConfig{{Name: "shop-dev", Tier: "dev", Token: "t"}},
		}
	}
	cases := []struct {
		name   string
		mutate func(*Config)
		ok     bool
	}{
		{"valid", func(*Config) {}, true},
		{"http public url", func(c *Config) { c.PublicURL = "http://mcp.example.com" }, false},
		{"http localhost", func(c *Config) { c.PublicURL = "http://localhost:8080" }, true},
		{"public url with path", func(c *Config) { c.PublicURL = "https://mcp.example.com/x" }, false},
		{"bad owner hash", func(c *Config) { c.OwnerTokenHash = "abc" }, false},
		{"underscore in name", func(c *Config) { c.Projects[0].Name = "shop_dev" }, false},
		{"long name", func(c *Config) { c.Projects[0].Name = strings.Repeat("a", 33) }, false},
		{"prod stack as dev", func(c *Config) { c.Projects[0].Stack = "shop-prod" }, false},
		{"missing token", func(c *Config) { c.Projects[0].Token = "" }, false},
		{"duplicate", func(c *Config) { c.Projects = append(c.Projects, c.Projects[0]) }, false},
		{"bad approval", func(c *Config) { c.Projects[0].Approval = "agent" }, false},
		{"no projects", func(c *Config) { c.Projects = nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(&c)
			err := c.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	c := base()
	_ = c.Validate()
	if c.Projects[0].Approval != ApprovalAlways || c.Projects[0].Stack != "shop-dev" {
		t.Fatalf("defaults not applied: %+v", c.Projects[0])
	}
}

func TestRedirectURIRules(t *testing.T) {
	for _, u := range []string{"https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1:3333/cb", "http://localhost/cb", "com.example.app://oauth"} {
		if err := validateRedirectURI(u); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
	for _, u := range []string{"javascript:alert(1)", "http://evil.example/cb", "https://x/cb#frag", "data:text/html,x", "https://user:pw@x/cb", "file:///etc/passwd"} {
		if err := validateRedirectURI(u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	if !redirectMatches("http://127.0.0.1/cb", "http://127.0.0.1:5555/cb") {
		t.Error("loopback port should be flexible")
	}
	if redirectMatches("https://claude.ai/cb", "https://claude.ai/cb/../evil") || redirectMatches("http://127.0.0.1/cb", "http://127.0.0.1:1/other") {
		t.Error("mismatched redirect accepted")
	}
}

func mustOpen(t *testing.T, path string) io.Reader {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}
