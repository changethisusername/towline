package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
)

// deployCapture records what reaches the bottom of the middleware chain.
type deployCapture struct {
	mu      sync.Mutex
	deploys []capturedDeploy
	stacks  string // listLocalStacks result
}

type capturedDeploy struct {
	Caller string
	File   string
	Args   map[string]any
}

func (d *deployCapture) list() []capturedDeploy {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]capturedDeploy(nil), d.deploys...)
}

// newAutoDeployBuilder builds projects whose tools are wrapped in the real
// caller gate, compose policy and tier gating (with a real approval store
// and the gateway's approval server), like buildProject.
func newAutoDeployBuilder(d *deployCapture) ProjectBuilder {
	return func(g *Gateway, p ProjectConfig) (*BuiltProject, error) {
		store := approval.NewStore()
		gating := middleware.TierProd
		if p.Approval == ApprovalProd {
			gating = middleware.Tier(p.Tier)
		}
		gate := &middleware.ApprovalGate{Mode: middleware.ApprovalHuman, Store: store, Project: p.Stack, Approver: g.approvals, ApprovalURL: g.cfg.PublicURL + "/ui"}
		wrap := func(name string, h server.ToolHandlerFunc) server.ServerTool {
			return server.ServerTool{
				Tool: mcp.NewTool(name),
				Handler: middleware.Chain(h,
					middleware.NewCallerGate(name, true),
					middleware.NewComposePolicy(name, p.ComposePolicy.Policy(p.Stack)),
					middleware.NewTierGating(gating, gate, name)),
			}
		}
		tools := map[string]server.ServerTool{
			"listLocalStacks": wrap("listLocalStacks", func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				d.mu.Lock()
				defer d.mu.Unlock()
				return mcp.NewToolResultText(d.stacks), nil
			}),
			"updateLocalStack": wrap("updateLocalStack", func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				c, _ := middleware.CallerFromContext(ctx)
				args := req.GetArguments()
				file, _ := args["file"].(string)
				d.mu.Lock()
				d.deploys = append(d.deploys, capturedDeploy{Caller: c.ID, File: file, Args: args})
				d.mu.Unlock()
				return mcp.NewToolResultText("Local stack updated successfully"), nil
			}),
			"createLocalStack": wrap("createLocalStack", func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				d.mu.Lock()
				defer d.mu.Unlock()
				d.deploys = append(d.deploys, capturedDeploy{Caller: "CREATE"})
				return mcp.NewToolResultText("created"), nil
			}),
		}
		return &BuiltProject{Tools: tools, Close: store.Stop, Approvals: store}, nil
	}
}

type adHarness struct {
	*Gateway
	gh      *fakeGitHub
	cap     *deployCapture
	auditFn func() string
}

const composeA = "services:\n  web:\n    image: nginx:1\n"
const composeB = "services:\n  web:\n    image: nginx:2\n"
const composeC = "services:\n  web:\n    image: nginx:3\n"

func newAutoDeployHarness(t *testing.T, gh *fakeGitHub, dataDir string, approvalPolicy string, mutate func(*ProjectConfig)) *adHarness {
	t.Helper()
	p := ProjectConfig{Name: "alpha", Stack: "alpha-dev", Tier: "dev", Token: "ptr_a", Approval: approvalPolicy,
		AutoDeploy: &AutoDeployConfig{Repo: "acme/shop", CommitEnv: "GIT_SHA"}}
	if mutate != nil {
		mutate(&p)
	}
	cfg := &Config{
		PublicURL:      "http://127.0.0.1:9",
		DataDir:        dataDir,
		OwnerTokenHash: approval.HashToken(testOwnerToken),
		Portainer:      PortainerConfig{URL: "https://portainer.invalid"},
		Projects:       []ProjectConfig{p, {Name: "beta", Stack: "beta-prod", Tier: "prod", Token: "ptr_b"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var buf strings.Builder
	audit := zerolog.New(zerolog.SyncWriter(&lockedWriter{mu: &mu, w: &buf}))
	d := &deployCapture{stacks: `[{"id":1,"name":"alpha-dev","endpoint_id":3,"env":[{"name":"X","value":"y"}]}]`}
	g, err := New(cfg, Options{Builder: newAutoDeployBuilder(d), Audit: &audit, github: gh.client(""), manualAutoDeploy: true})
	if err != nil {
		t.Fatal(err)
	}
	if g.autodeploy != nil {
		g.autodeploy.settle = 0
	}
	t.Cleanup(g.Close)
	return &adHarness{Gateway: g, gh: gh, cap: d, auditFn: func() string { mu.Lock(); defer mu.Unlock(); return buf.String() }}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (h *adHarness) tick(t *testing.T) {
	t.Helper()
	h.autodeploy.check(context.Background(), h.autodeploy.projects["alpha"])
}

func (h *adHarness) status() string {
	p := h.autodeploy.projects["alpha"]
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (h *adHarness) record() AutoDeployRecord {
	r, _ := h.store.AutoDeployRecord("alpha")
	return r
}

func pendingRecords(g *Gateway) []approval.Record {
	var out []approval.Record
	for _, r := range g.approvals.Records() {
		if r.Status == approval.StatusPending {
			out = append(out, r)
		}
	}
	return out
}

// startedRepo is a fake GitHub with acme/shop@main at A.
func startedRepo(t *testing.T) *fakeGitHub {
	gh := newFakeGitHub(t)
	gh.commit(shaA, "docker-compose.yml", composeA, "first")
	gh.commit(shaB, "docker-compose.yml", composeB, "feat: nginx 2\nlong body\x1b[31m")
	gh.commit(shaC, "docker-compose.yml", composeC, "feat: nginx 3")
	gh.setHead("acme/shop", "main", shaA)
	return gh
}

func TestAutoDeployBaselineDoesNotDeploy(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.tick(t)
	h.tick(t)
	if n := len(h.cap.list()); n != 0 {
		t.Fatalf("baseline deployed %d times", n)
	}
	if r := h.record(); r.DeployedSHA != shaA {
		t.Fatalf("record %+v", r)
	}
	if !strings.Contains(h.status(), "up to date") {
		t.Fatalf("status %q", h.status())
	}
	if !strings.Contains(h.auditFn(), `"stage":"baseline"`) {
		t.Fatal("baseline not audited")
	}
}

func TestAutoDeployWithoutApprovalOnDev(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.tick(t)
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	deploys := h.cap.list()
	if len(deploys) != 1 {
		t.Fatalf("deploys: %+v", deploys)
	}
	d := deploys[0]
	if d.Caller != "autodeploy:alpha" || d.File != composeB || d.Args["id"] != float64(1) || d.Args["environmentId"] != float64(3) || d.Args["pullImage"] != true {
		t.Fatalf("deploy %+v", d)
	}
	if r := h.record(); r.DeployedSHA != shaB || r.DeployedAt.IsZero() {
		t.Fatalf("record %+v", r)
	}
	h.tick(t)
	if len(h.cap.list()) != 1 {
		t.Fatal("redeployed the same commit")
	}
	audit := h.auditFn()
	for _, want := range []string{`"connection":"autodeploy:alpha"`, `"source":"auto-deploy"`, `"commit":"` + shaB + `"`, `"stage":"deployed"`} {
		if !strings.Contains(audit, want) {
			t.Fatalf("audit missing %s:\n%s", want, audit)
		}
	}
	if strings.Contains(audit, "nginx:2") {
		t.Fatal("audit log holds the compose file")
	}
}

func TestAutoDeployApprovalFlow(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalAlways, nil)
	h.tick(t)
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	if len(h.cap.list()) != 0 {
		t.Fatal("deployed before approval")
	}
	recs := pendingRecords(h.Gateway)
	if len(recs) != 1 {
		t.Fatalf("approval requests: %+v", recs)
	}
	rec := recs[0]
	if rec.Client != "autodeploy:alpha" || rec.StackContent != composeB || rec.Project != "alpha-dev" {
		t.Fatalf("request %+v", rec)
	}
	for _, want := range []string{shaB, "Subject: feat: nginx 2", "acme/shop", "Arguments:"} {
		if !strings.Contains(rec.Description, want) {
			t.Fatalf("description missing %q:\n%s", want, rec.Description)
		}
	}
	if strings.Contains(rec.Description, "long body") || strings.Contains(rec.Description, "\x1b") {
		t.Fatalf("subject not sanitized:\n%s", rec.Description)
	}
	// Waiting: no second request.
	h.tick(t)
	if len(h.approvals.Records()) != 1 || !strings.Contains(h.status(), "waiting for your approval") {
		t.Fatalf("status %q, %d records", h.status(), len(h.approvals.Records()))
	}

	if err := h.approvals.Decide(rec.ID, approval.StatusApproved); err != nil {
		t.Fatal(err)
	}
	h.tick(t)
	deploys := h.cap.list()
	if len(deploys) != 1 || deploys[0].File != composeB || deploys[0].Caller != "autodeploy:alpha" {
		t.Fatalf("deploys after approval: %+v", deploys)
	}
	h.tick(t)
	h.tick(t)
	if len(h.cap.list()) != 1 || h.record().DeployedSHA != shaB {
		t.Fatalf("deploys %d, record %+v", len(h.cap.list()), h.record())
	}
}

func TestAutoDeployRejection(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalAlways, nil)
	h.tick(t)
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	rec := pendingRecords(h.Gateway)[0]
	if err := h.approvals.Decide(rec.ID, approval.StatusRejected); err != nil {
		t.Fatal(err)
	}
	h.tick(t)
	h.tick(t)
	if len(h.cap.list()) != 0 || len(h.approvals.Records()) != 1 {
		t.Fatalf("rejected commit deployed or asked again: %d deploys, %d requests", len(h.cap.list()), len(h.approvals.Records()))
	}
	if r := h.record(); r.RejectedSHA != shaB || r.DeployedSHA != shaA {
		t.Fatalf("record %+v", r)
	}
	// A newer commit asks again.
	h.gh.setHead("acme/shop", "main", shaC)
	h.tick(t)
	if p := pendingRecords(h.Gateway); len(p) != 1 || p[0].StackContent != composeC {
		t.Fatalf("no request for the newer commit: %+v", p)
	}
}

func TestAutoDeploySupersedes(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalAlways, nil)
	h.tick(t)
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	old := pendingRecords(h.Gateway)[0]
	h.gh.setHead("acme/shop", "main", shaC)
	h.tick(t)
	if s, _ := h.approvals.Status(context.Background(), old.ID); s != approval.StatusRejected {
		t.Fatalf("superseded request status %s", s)
	}
	// Approving the superseded request is impossible.
	if err := h.approvals.Decide(old.ID, approval.StatusApproved); err == nil {
		t.Fatal("superseded request approved")
	}
	p := pendingRecords(h.Gateway)
	if len(p) != 1 || p[0].StackContent != composeC {
		t.Fatalf("pending after supersede: %+v", p)
	}

	// The owner page shows it as superseded, and who asked.
	rr := httptest.NewRecorder()
	h.ui.render(rr, 200, "dashboard", h.ui.dashboard(ownerSession{csrf: "x"}, ""))
	page := rr.Body.String()
	for _, want := range []string{"superseded", "auto-deploy (acme/shop)", "Deploy latest now", "acme/shop@main:docker-compose.yml"} {
		if !strings.Contains(page, want) {
			t.Fatalf("dashboard missing %q", want)
		}
	}

	// The branch moving back to the deployed commit withdraws the request.
	h.gh.setHead("acme/shop", "main", shaA)
	h.tick(t)
	if len(pendingRecords(h.Gateway)) != 0 || len(h.cap.list()) != 0 {
		t.Fatal("request not withdrawn when the branch moved back")
	}
}

func TestAutoDeployWaitsForChecks(t *testing.T) {
	cases := []struct {
		name   string
		runs   []map[string]any
		settle time.Duration
		want   string
	}{
		{"no checks yet", []map[string]any{}, 0, "waiting for checks"},
		{"in progress", []map[string]any{{"status": "in_progress"}}, 0, "waiting for checks"},
		{"failing", []map[string]any{{"status": "completed", "conclusion": "failure"}}, 0, "checks failed"},
		{"not settled", []map[string]any{{"status": "completed", "conclusion": "success"}}, time.Hour, "waiting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := startedRepo(t)
			h := newAutoDeployHarness(t, gh, t.TempDir(), ApprovalProd, nil)
			h.autodeploy.settle = tc.settle
			h.tick(t)
			gh.setRuns(shaB, tc.runs...)
			gh.setHead("acme/shop", "main", shaB)
			h.tick(t)
			h.tick(t)
			if len(h.cap.list()) != 0 || len(h.approvals.Records()) != 0 {
				t.Fatal("deployed without passing, settled checks")
			}
			if !strings.Contains(h.status(), tc.want) {
				t.Fatalf("status %q", h.status())
			}
			if tc.name == "failing" && h.record().FailedSHA != "" {
				t.Fatal("failing checks mark the commit failed (a re-run could pass)")
			}
		})
	}

	// require_checks: false deploys without them.
	gh := startedRepo(t)
	f := false
	h := newAutoDeployHarness(t, gh, t.TempDir(), ApprovalProd, func(p *ProjectConfig) { p.AutoDeploy.RequireChecks = &f })
	h.tick(t)
	gh.setRuns(shaB)
	gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	if len(h.cap.list()) != 1 {
		t.Fatal("require_checks false still waited")
	}
}

func TestAutoDeployNeverCreatesStacks(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.cap.stacks = `[]`
	h.tick(t)
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	if len(h.cap.list()) != 0 {
		t.Fatalf("calls: %+v", h.cap.list())
	}
	if !strings.Contains(h.status(), "not found") {
		t.Fatalf("status %q", h.status())
	}
	// Another stack's entry (were scoping ever to fail) is not used either.
	h.cap.stacks = `[{"id":2,"name":"beta-prod","endpoint_id":3}]`
	h.tick(t)
	if len(h.cap.list()) != 0 {
		t.Fatal("deployed to another stack")
	}
	// The watcher can't reach any tool but its two.
	if _, err := h.mcp.callInternal(context.Background(), "alpha", middleware.Caller{ID: "autodeploy:alpha", Scope: middleware.ScopeDeploy}, "createLocalStack", map[string]any{}); err == nil {
		t.Fatal("callInternal ran createLocalStack")
	}
	if _, err := h.mcp.callInternal(context.Background(), "alpha", middleware.Caller{ID: "conn_x", Scope: middleware.ScopeDeploy}, "updateLocalStack", map[string]any{}); err == nil {
		t.Fatal("callInternal ran for a non-watcher caller")
	}
}

func TestAutoDeployPolicyRefusalIsNotRetried(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalAlways, nil)
	h.tick(t)
	h.gh.commit(shaB, "docker-compose.yml", "services:\n  web:\n    image: nginx\n    privileged: true\n", "evil")
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	h.tick(t)
	if len(h.approvals.Records()) != 0 || len(h.cap.list()) != 0 {
		t.Fatal("privileged compose reached approval or the stack")
	}
	r := h.record()
	if r.FailedSHA != shaB || !strings.Contains(r.LastError, "privileged") {
		t.Fatalf("record %+v", r)
	}
	if n := strings.Count(h.auditFn(), `"stage":"failed"`); n != 1 {
		t.Fatalf("failed %d times (retry loop?)", n)
	}
}

func TestAutoDeployStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	gh := startedRepo(t)
	h := newAutoDeployHarness(t, gh, dir, ApprovalProd, nil)
	h.tick(t)
	gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	if len(h.cap.list()) != 1 {
		t.Fatal("no deploy")
	}
	h.Close()

	h2 := newAutoDeployHarness(t, gh, dir, ApprovalProd, nil)
	h2.tick(t)
	if len(h2.cap.list()) != 0 {
		t.Fatal("redeployed after restart")
	}

	// A different repository starts from a new baseline.
	h2.Close()
	gh.setHead("acme/other", "main", shaC)
	h3 := newAutoDeployHarness(t, gh, dir, ApprovalProd, func(p *ProjectConfig) { p.AutoDeploy.Repo = "acme/other" })
	h3.tick(t)
	if len(h3.cap.list()) != 0 || h3.record().DeployedSHA != shaC {
		t.Fatalf("repo change deployed: %+v", h3.record())
	}

	// Turning auto-deploy off forgets the record.
	h3.Close()
	h4 := newAutoDeployHarness(t, gh, dir, ApprovalProd, func(p *ProjectConfig) { p.AutoDeploy = nil })
	if _, ok := h4.store.AutoDeployRecord("alpha"); ok || h4.autodeploy != nil {
		t.Fatal("record kept after auto-deploy was turned off")
	}
}

func TestAutoDeployTokenIsBoundToWatcher(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalAlways, nil)
	h.tick(t)
	h.gh.setHead("acme/shop", "main", shaB)
	h.tick(t)
	pd := h.autodeploy.projects["alpha"].peekPending()
	if pd == nil {
		t.Fatal("no pending deploy")
	}
	if err := h.approvals.Decide(pd.RequestID, approval.StatusApproved); err != nil {
		t.Fatal(err)
	}
	// A deploy connection replaying the watcher's token gets nothing.
	args := map[string]any{"approvalToken": pd.Token}
	for k, v := range pd.Args {
		args[k] = v
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = "updateLocalStack"
	req.Params.Arguments = args
	ctx := middleware.WithCaller(context.Background(), middleware.Caller{ID: "conn_attacker", Scope: middleware.ScopeDeploy})
	res, err := h.mcp.built["alpha"].Tools["updateLocalStack"].Handler(ctx, req)
	if err != nil || !res.IsError || !strings.Contains(resultText(res), "different client") {
		t.Fatalf("replayed token: %v %s", err, resultText(res))
	}
	if len(h.cap.list()) != 0 {
		t.Fatal("replayed token deployed")
	}
	// The watcher still deploys it.
	h.tick(t)
	if len(h.cap.list()) != 1 {
		t.Fatal("watcher could not use its token after the replay attempt")
	}
}

func TestAutoDeployLatestNow(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.tick(t)
	if len(h.cap.list()) != 0 {
		t.Fatal("baseline deployed")
	}
	if err := h.autodeploy.forceLatest("alpha"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.autodeploy.projects["alpha"].nudge:
	default:
		t.Fatal("forceLatest did not nudge")
	}
	h.tick(t)
	if d := h.cap.list(); len(d) != 1 || d[0].File != composeA {
		t.Fatalf("deploys %+v", d)
	}
	h.tick(t)
	if len(h.cap.list()) != 1 {
		t.Fatal("force stayed on")
	}
	if err := h.autodeploy.forceLatest("beta"); err == nil {
		t.Fatal("forced a project without auto-deploy")
	}
}

func TestAutoDeployOwnerUIForm(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	// Drive the real handler (host, origin and CSRF checks) with a
	// logged-in session.
	h.ui.mu.Lock()
	h.ui.sessions[hashSecret("sess")] = ownerSession{csrf: "csrf-token", expires: time.Now().Add(time.Hour)}
	h.ui.mu.Unlock()
	post := func(form string, origin string) int {
		req := httptest.NewRequest("POST", "http://127.0.0.1:9/ui/autodeploy", strings.NewReader(form))
		req.Host = "127.0.0.1:9"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: ownerCookie, Value: "sess"})
		rr := httptest.NewRecorder()
		h.Handler().ServeHTTP(rr, req)
		return rr.Code
	}
	if c := post("project=alpha&csrf=wrong", "http://127.0.0.1:9"); c != 403 {
		t.Fatalf("bad csrf: %d", c)
	}
	if c := post("project=alpha&csrf=csrf-token", "https://evil.example"); c != 403 {
		t.Fatalf("cross-origin: %d", c)
	}
	if h.autodeploy.projects["alpha"].forced() {
		t.Fatal("refused form still forced a deploy")
	}
	if c := post("project=alpha&csrf=csrf-token", "http://127.0.0.1:9"); c != 303 {
		t.Fatalf("valid form: %d", c)
	}
	if !h.autodeploy.projects["alpha"].forced() {
		t.Fatal("form did not request a deploy")
	}
	if c := post("project=beta&csrf=csrf-token", "http://127.0.0.1:9"); c != 400 {
		t.Fatalf("project without auto-deploy: %d", c)
	}
}

func TestAutoDeployGuideMentionsIt(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	g := h.mcp.guide(&Connection{Name: "x", Scope: middleware.ScopeDeploy, Projects: []string{"alpha", "beta"}}, "")
	if !strings.Contains(g, "Auto-deploy: merges to `acme/shop`@`main`") {
		t.Fatalf("guide:\n%s", g)
	}
	if strings.Count(g, "Auto-deploy:") != 1 {
		t.Fatal("auto-deploy listed for a project without it")
	}
}

func TestAutoDeployStackListParsing(t *testing.T) {
	// listLocalStacks output is JSON from the scoped tool.
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.cap.stacks = `not json`
	_, _, err := h.autodeploy.findStack(context.Background(), h.autodeploy.projects["alpha"])
	if err == nil {
		t.Fatal("invalid stack list accepted")
	}
	b, _ := json.Marshal([]map[string]any{{"id": 7, "name": "alpha-dev", "endpoint_id": 4}})
	h.cap.stacks = string(b)
	id, env, err := h.autodeploy.findStack(context.Background(), h.autodeploy.projects["alpha"])
	if err != nil || id != 7 || env != 4 {
		t.Fatalf("findStack = %d %d %v", id, env, err)
	}
}

// The background loop polls, reacts to nudges and stops on Close.
func TestAutoDeployLoop(t *testing.T) {
	gh := startedRepo(t)
	d := &deployCapture{stacks: `[{"id":1,"name":"alpha-dev","endpoint_id":3}]`}
	cfg := &Config{
		PublicURL:      "http://127.0.0.1:9",
		DataDir:        t.TempDir(),
		OwnerTokenHash: approval.HashToken(testOwnerToken),
		Portainer:      PortainerConfig{URL: "https://portainer.invalid"},
		Projects: []ProjectConfig{{Name: "alpha", Stack: "alpha-dev", Tier: "dev", Token: "ptr_a", Approval: ApprovalProd,
			AutoDeploy: &AutoDeployConfig{Repo: "acme/shop"}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	audit := zerolog.Nop()
	g, err := New(cfg, Options{Builder: newAutoDeployBuilder(d), Audit: &audit, github: gh.client(""), pollInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	g.autodeploy.settle = 0
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("baseline", func() bool { r, ok := g.store.AutoDeployRecord("alpha"); return ok && r.DeployedSHA == shaA })
	gh.setHead("acme/shop", "main", shaB)
	waitFor("deploy", func() bool { return len(d.list()) == 1 })
	g.Close()
	n := gh.count("/repos/")
	time.Sleep(60 * time.Millisecond)
	if gh.count("/repos/") != n {
		t.Fatal("watcher kept polling after Close")
	}
}
