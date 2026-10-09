package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/middleware"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestSameOriginWithBrowserHeaders(t *testing.T) {
	tg := newTestGateway(t)
	cases := []struct {
		name   string
		origin string
		site   string
		want   bool
	}{
		{"own origin", tg.url, "same-origin", true},
		{"null origin from our own page", "null", "same-origin", true},
		{"null origin from another site", "null", "cross-site", false},
		{"null origin without fetch metadata", "null", "", false},
		{"no origin, not a browser", "", "", true},
		{"no origin, cross-site", "", "cross-site", false},
		{"no origin, same-site subdomain", "", "same-site", false},
		{"other origin", "https://evil.example", "cross-site", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := http.NewRequest("POST", tg.url+"/ui/login", nil)
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if c.site != "" {
				r.Header.Set("Sec-Fetch-Site", c.site)
			}
			if got := tg.sameOrigin(r); got != c.want {
				t.Fatalf("sameOrigin = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPagesKeepOriginOnFormPosts(t *testing.T) {
	tg := newTestGateway(t)
	resp, err := http.Get(tg.url + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// no-referrer would make browsers send "Origin: null" on form posts.
	if rp := resp.Header.Get("Referrer-Policy"); rp != "same-origin" {
		t.Fatalf("Referrer-Policy %q", rp)
	}
}

func TestConsentPageAllowsTheCallbackAsFormTarget(t *testing.T) {
	tg := newTestGateway(t)
	known := func(n string) bool { _, ok := tg.cfg.Project(n); return ok }
	if _, err := tg.store.OpenPairing("phone", []string{"alpha"}, middleware.ScopeRead, known); err != nil {
		t.Fatal(err)
	}
	clientID, _, err := tg.store.registerClient("ChatGPT", []string{"https://chatgpt.com/connector_platform_oauth_redirect"}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce()
	resp, err := noRedirectClient().Get(authorizeURL(tg.url, clientID, "https://chatgpt.com/connector_platform_oauth_redirect", challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Chrome checks the consent page's form-action against the redirect
	// that follows the post.
	csp := resp.Header.Get("Content-Security-Policy")
	if resp.StatusCode != http.StatusOK || !strings.Contains(csp, "form-action 'self' https://chatgpt.com;") {
		t.Fatalf("consent page: %d CSP %q", resp.StatusCode, csp)
	}
}

func TestChatGPTCallbackIsADefault(t *testing.T) {
	tg := newTestGateway(t)
	c := tg.createConn(t, KindOAuth, middleware.ScopeRead, "alpha")
	if _, ok := pickRedirect(tg.store.Connection(c.ID).RedirectURIs, "https://chatgpt.com/connector_platform_oauth_redirect"); !ok {
		t.Fatal("ChatGPT callback not allowed by default")
	}
}

func TestResourceSpellings(t *testing.T) {
	tg := newTestGateway(t)
	u, _ := url.Parse(tg.url)
	upper := "HTTP://" + strings.ToUpper(u.Host)
	cases := []struct {
		name     string
		resource string
		want     string
		wantErr  bool
	}{
		{"omitted", "", tg.url + "/mcp", false},
		{"canonical", tg.url + "/mcp", tg.url + "/mcp", false},
		{"trailing slash", tg.url + "/mcp/", tg.url + "/mcp", false},
		{"bare origin (ChatGPT)", tg.url, tg.url + "/mcp", false},
		{"bare origin with slash", tg.url + "/", tg.url + "/mcp", false},
		{"uppercase host", upper + "/mcp", tg.url + "/mcp", false},
		{"project endpoint", tg.url + "/p/alpha/mcp", tg.url + "/p/alpha/mcp", false},
		{"unknown project", tg.url + "/p/nope/mcp", "", true},
		{"other host", "https://evil.example/mcp", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := tg.checkResource(c.resource, nil)
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("checkResource(%q) = %q, %v", c.resource, got, err)
			}
		})
	}
	if got := tg.tokenResource(tg.url); got != tg.url+"/mcp" {
		t.Fatalf("tokenResource(bare origin) = %q", got)
	}
}

func TestPublicURLIsCanonical(t *testing.T) {
	cfg := &Config{
		PublicURL:      "https://MCP.Example.com:443/",
		OwnerTokenHash: strings.Repeat("ab", 32),
		Portainer:      PortainerConfig{URL: "https://portainer.invalid"},
		Projects:       []ProjectConfig{{Name: "alpha", Stack: "alpha-dev", Tier: "dev", Token: "x"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://mcp.example.com" {
		t.Fatalf("PublicURL %q", cfg.PublicURL)
	}
}

func TestProjectNamesFitClaudeToolNameLimit(t *testing.T) {
	longest := strings.Repeat("a", 24)
	if !ValidProjectName(longest) || ValidProjectName(longest+"a") {
		t.Fatal("project names must be capped at 24 characters")
	}
	// Claude Code names a tool mcp__<server>__<tool>; the API allows 64.
	if n := len("mcp__towline__" + longest + toolSeparator + "towline_domains_remove"); n > 64 {
		t.Fatalf("longest tool name is %d characters", n)
	}
}

func TestWritingToolsAreNotMarkedReadOnly(t *testing.T) {
	tg := newTestGateway(t)
	tok := tg.createConn(t, KindToken, middleware.ScopeDeploy, "alpha")
	for _, endpoint := range []string{"/mcp", "/p/alpha/mcp"} {
		c := mcpClient(t, tg.url+endpoint, tok.Secret)
		res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, tool := range res.Tools {
			name := tool.Name
			if _, after, ok := strings.Cut(name, toolSeparator); ok {
				name = after
			}
			ro := tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint
			switch name {
			case "dockerProxy", "updateLocalStack":
				seen++
				if ro {
					t.Fatalf("%s %s is marked read-only", endpoint, tool.Name)
				}
			case "towline_service_health":
				if !ro {
					t.Fatalf("%s %s lost its read-only hint", endpoint, tool.Name)
				}
			}
		}
		if seen != 2 {
			t.Fatalf("%s: tools %v", endpoint, res.Tools)
		}
	}
}

func TestSlowToolCallsAnswerBeforeTheTunnelTimesOut(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan bool, 1)
	h := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-release
		finished <- ctx.Err() == nil
		return mcp.NewToolResultText("done"), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, done, err := runDetached(ctx, mcp.CallToolRequest{}, h, 20*time.Millisecond)
	if done || err != nil {
		t.Fatalf("slow call: done=%v err=%v", done, err)
	}
	// The client's request ends; the deploy keeps going.
	cancel()
	close(release)
	if ok := <-finished; !ok {
		t.Fatal("handler context was cancelled with the request")
	}

	quick := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("quick"), nil
	}
	res, done, err := runDetached(context.Background(), mcp.CallToolRequest{}, quick, time.Second)
	if !done || err != nil || res == nil {
		t.Fatalf("quick call: %v %v %v", res, done, err)
	}
}

func TestConsentWithSeveralWindowsHasNoDefault(t *testing.T) {
	tg := newTestGateway(t)
	known := func(n string) bool { _, ok := tg.cfg.Project(n); return ok }
	if _, err := tg.store.OpenPairing("decoy", []string{"alpha"}, middleware.ScopeRead, known); err != nil {
		t.Fatal(err)
	}
	laptop, err := tg.store.OpenPairing("laptop", []string{"beta"}, middleware.ScopeDeploy, known)
	if err != nil {
		t.Fatal(err)
	}
	clientID, _, err := tg.store.registerClient("Claude", []string{"https://claude.ai/api/mcp/auth_callback"}, false)
	if err != nil {
		t.Fatal(err)
	}
	owner := noRedirectClient()
	if resp, _ := postForm(t, owner, tg.url+"/ui/login", url.Values{"token": {testOwnerToken}}, "", ""); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("owner login: %d", resp.StatusCode)
	}
	_, challenge := pkce()
	resp, err := owner.Get(authorizeURL(tg.url, clientID, "https://claude.ai/api/mcp/auth_callback", challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), `<option value="" disabled selected>`) {
		t.Fatal("with two windows open, one is preselected")
	}
	reqID := hiddenRequest.FindStringSubmatch(string(page))[1]
	// Approving without choosing a window approves nothing; the choice made
	// in a post is kept when the page is shown again.
	form := url.Values{"request": {reqID}, "decision": {"approve"}}
	req, _ := http.NewRequest("POST", tg.url+"/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", tg.url)
	resp, err = owner.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Location") != "" {
		t.Fatal("approved without a chosen window")
	}
	r, _ := http.NewRequest("POST", "/", strings.NewReader("pairing="+laptop.ID))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(tg.url)
	for _, c := range owner.Jar.Cookies(u) {
		r.AddCookie(c)
	}
	_ = r.ParseForm()
	rec := httptest.NewRecorder()
	tg.renderConsent(rec, r, &authzRequest{ID: "x", RedirectURI: "https://claude.ai/cb"}, nil, "Wrong owner token.")
	if !strings.Contains(rec.Body.String(), `value="`+laptop.ID+`" selected`) {
		t.Fatal("chosen window not kept after an error")
	}
}

func TestConnectionNamesCountCharacters(t *testing.T) {
	tg := newTestGateway(t)
	known := func(n string) bool { _, ok := tg.cfg.Project(n); return ok }
	for _, name := range []string{strings.Repeat("я", 64), strings.Repeat("🚀", 30)} {
		if _, err := tg.store.CreateConnection(NewConnectionRequest{Name: name, Kind: KindToken, Projects: []string{"alpha"}, Scope: middleware.ScopeRead}, known); err != nil {
			t.Fatalf("%d-character name refused: %v", len([]rune(name)), err)
		}
	}
	if _, err := tg.store.CreateConnection(NewConnectionRequest{Name: strings.Repeat("я", 65), Kind: KindToken, Projects: []string{"alpha"}, Scope: middleware.ScopeRead}, known); err == nil {
		t.Fatal("65-character name accepted")
	}
}
