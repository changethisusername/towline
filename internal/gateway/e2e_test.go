package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rs/zerolog"
)

// mockPortainer serves two projects' stacks. Each project token sees only
// its own stack, like Portainer's team scoping, and every request is
// recorded with the token that made it.
type mockPortainer struct {
	mu       sync.Mutex
	requests []string // "<token> <method> <path>"
	files    map[int]string
}

var mockStacks = map[string]map[string]any{
	"tok-alpha": {"Id": 1, "Name": "alpha-dev", "Type": 2, "Status": 1, "EndpointId": 3},
	"tok-beta":  {"Id": 2, "Name": "beta-prod", "Type": 2, "Status": 1, "EndpointId": 3},
}

func (m *mockPortainer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-API-Key")
	m.mu.Lock()
	m.requests = append(m.requests, token+" "+r.Method+" "+r.URL.Path)
	m.mu.Unlock()
	own, ok := mockStacks[token]
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := own["Id"].(int)
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/stacks":
		_ = json.NewEncoder(w).Encode([]any{own})
	case r.Method == "GET" && r.URL.Path == "/api/stacks/"+itoa(id):
		_ = json.NewEncoder(w).Encode(own)
	case r.Method == "GET" && r.URL.Path == "/api/stacks/"+itoa(id)+"/file":
		m.mu.Lock()
		f := m.files[id]
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"StackFileContent": f})
	case r.Method == "PUT" && r.URL.Path == "/api/stacks/"+itoa(id):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.files[id], _ = body["stackFileContent"].(string)
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(own)
	default:
		// Anything else (another project's stack included) is forbidden,
		// as Portainer would for a team-scoped key.
		http.Error(w, "forbidden", http.StatusForbidden)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func (m *mockPortainer) file(id int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.files[id]
}

func (m *mockPortainer) count(prefix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func newE2EGateway(t *testing.T) (*testGateway, *mockPortainer) {
	t.Helper()
	mp := &mockPortainer{files: map[int]string{1: "services:\n  web:\n    image: nginx\n", 2: "services:\n  api:\n    image: api:1\n"}}
	portainer := httptest.NewServer(mp)
	t.Cleanup(portainer.Close)

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
	var auditBuf strings.Builder
	audit := zerolog.New(&auditBuf)
	g, err := New(cfg, Options{Audit: &audit})
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = g.Handler()
	srv.Start()
	t.Cleanup(func() { srv.Close(); g.Close() })
	return &testGateway{Gateway: g, srv: srv, url: public, auditFn: auditBuf.String}, mp
}

func callArgs(t *testing.T, c *client.Client, name string, args map[string]any) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
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

var approvalTokenRe = regexp.MustCompile(`approvalToken: "([0-9a-f]+)"`)

func TestEndToEndWithRealProjects(t *testing.T) {
	tg, mp := newE2EGateway(t)
	deployBoth := tg.createConn(t, KindToken, middleware.ScopeDeploy, "alpha-dev", "beta-prod")
	readAlpha := tg.createConn(t, KindToken, middleware.ScopeRead, "alpha-dev")
	otherBeta := tg.createConn(t, KindToken, middleware.ScopeDeploy, "beta-prod")

	c := mcpClient(t, tg.url+"/mcp", deployBoth.Secret)
	names := strings.Join(toolNames(t, c), ",")
	for _, want := range []string{"alpha-dev__updateLocalStack", "beta-prod__towline_service_health", "beta-prod__dockerProxy", guideTool} {
		if !strings.Contains(names, want) {
			t.Fatalf("missing %s in %s", want, names)
		}
	}

	// The guide describes the projects for an agent that has no files.
	guide, _ := callArgs(t, c, guideTool, nil)
	if !strings.Contains(guide, "beta-prod") || !strings.Contains(guide, "owner's approval") || !strings.Contains(guide, "Towline DevOps Skill") {
		t.Fatalf("guide:\n%s", guide)
	}

	// Dev project with approval=prod: deploys run straight away, with the
	// project's own token.
	newAlpha := "services:\n  web:\n    image: nginx:1.27\n"
	out, isErr := callArgs(t, c, "alpha-dev__updateLocalStack", map[string]any{"id": 1, "environmentId": 3, "file": newAlpha})
	if isErr {
		t.Fatalf("alpha deploy: %s", out)
	}
	if mp.count("tok-alpha PUT /api/stacks/1") != 1 || mp.file(1) != newAlpha {
		t.Fatalf("alpha deploy did not reach Portainer: %v", mp.requests)
	}

	// The compose policy still applies remotely.
	out, isErr = callArgs(t, c, "alpha-dev__updateLocalStack", map[string]any{"id": 1, "environmentId": 3, "file": "services:\n  web:\n    image: nginx\n    privileged: true\n"})
	if !isErr || mp.count("tok-alpha PUT") != 1 {
		t.Fatalf("privileged compose was not refused: %s", out)
	}

	// A project's tool can't be pointed at another project's stack.
	out, isErr = callArgs(t, c, "alpha-dev__getLocalStackFile", map[string]any{"id": 2})
	if !isErr {
		t.Fatalf("alpha tool read beta's stack: %s", out)
	}
	if mp.count("tok-alpha GET /api/stacks/2") != 0 {
		t.Fatal("alpha's token was used on beta's stack")
	}

	// Prod project: the change waits for the owner.
	newBeta := "services:\n  api:\n    image: api:2\n"
	betaArgs := map[string]any{"id": 2, "environmentId": 3, "file": newBeta}
	out, isErr = callArgs(t, c, "beta-prod__updateLocalStack", betaArgs)
	m := approvalTokenRe.FindStringSubmatch(out)
	if isErr || m == nil || !strings.Contains(out, "human approval") {
		t.Fatalf("beta deploy should wait for approval: %s", out)
	}
	recs := tg.approvals.Records()
	if len(recs) != 1 || recs[0].Project != "beta-prod" || recs[0].Client != deployBoth.ID || recs[0].StackContent != newBeta {
		t.Fatalf("approval request: %+v", recs)
	}
	withToken := map[string]any{"id": 2, "environmentId": 3, "file": newBeta, "approvalToken": m[1]}
	if out, _ := callArgs(t, c, "beta-prod__updateLocalStack", withToken); !strings.Contains(out, "pending") {
		t.Fatalf("re-call before approval: %s", out)
	}
	if mp.count("tok-beta PUT") != 0 {
		t.Fatal("prod deploy ran before approval")
	}
	if err := tg.approvals.Decide(recs[0].ID, approval.StatusApproved); err != nil {
		t.Fatal(err)
	}
	// Another connection holding the token gets nothing.
	o := mcpClient(t, tg.url+"/p/beta-prod/mcp", otherBeta.Secret)
	if out, isErr := callArgs(t, o, "updateLocalStack", withToken); !isErr || !strings.Contains(out, "different client") {
		t.Fatalf("token reused by another connection: %s", out)
	}
	if out, isErr := callArgs(t, c, "beta-prod__updateLocalStack", withToken); isErr {
		t.Fatalf("approved deploy: %s", out)
	}
	if mp.count("tok-beta PUT /api/stacks/2") != 1 || mp.file(2) != newBeta {
		t.Fatalf("approved deploy did not reach Portainer: %v", mp.requests)
	}
	// Single use.
	if _, isErr := callArgs(t, c, "beta-prod__updateLocalStack", withToken); !isErr {
		t.Fatal("approval token reused")
	}

	// Read-only connection: can read, can't change, and Portainer never
	// sees a write.
	r := mcpClient(t, tg.url+"/p/alpha-dev/mcp", readAlpha.Secret)
	if out, isErr := callArgs(t, r, "getLocalStackFile", map[string]any{"id": 1}); isErr || !strings.Contains(out, "nginx:1.27") {
		t.Fatalf("read file: %s", out)
	}
	before := mp.count("tok-alpha PUT")
	if _, isErr := callArgs(t, r, "updateLocalStack", map[string]any{"id": 1, "environmentId": 3, "file": newAlpha}); !isErr {
		t.Fatal("read connection deployed")
	}
	if _, isErr := callArgs(t, r, "dockerProxy", map[string]any{"environmentId": 3, "method": "POST", "dockerAPIPath": "/containers/create"}); !isErr {
		t.Fatal("read connection made a docker POST")
	}
	if mp.count("tok-alpha PUT") != before || mp.count("tok-alpha POST") != 0 {
		t.Fatalf("read connection reached Portainer with a write: %v", mp.requests)
	}

	audit := tg.auditFn()
	for _, want := range []string{`"project":"beta-prod"`, `"tool":"updateLocalStack"`, deployBoth.ID} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit log missing %s", want)
		}
	}
	if strings.Contains(audit, newBeta) || strings.Contains(audit, deployBoth.Secret) {
		t.Fatal("audit log contains arguments or secrets")
	}
}

func TestEndToEndHTTPHeaders(t *testing.T) {
	tg, _ := newE2EGateway(t)
	resp, err := http.Get(tg.url + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var meta map[string]any
	_ = json.Unmarshal(body, &meta)
	if meta["issuer"] != tg.url || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("server metadata: %v %v", meta, resp.Header)
	}
	resp, _ = http.Get(tg.url + "/ui")
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("ui headers: %v", resp.Header)
	}
}
