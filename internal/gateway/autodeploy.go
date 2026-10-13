package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/mark3labs/mcp-go/mcp"
)

// Auto-deploy timing.
const (
	// pollWithToken and pollWithoutToken are how often GitHub is polled.
	// Unauthenticated requests are limited to 60 an hour per IP.
	pollWithToken    = 60 * time.Second
	pollWithoutToken = 300 * time.Second
	// checksSettle is how long a commit must have been the branch head
	// before it deploys when checks are required, so CI jobs that register
	// their checks late are not missed.
	checksSettle = 2 * time.Minute
	// minNudgeGap spaces out checks triggered by webhooks.
	minNudgeGap = 10 * time.Second

	maxSubjectRunes = 120
	maxErrorRunes   = 500
)

// autoDeployCallerPrefix starts the caller id of every auto-deploy. Real
// connection ids start with "conn_", so the two can never collide.
const autoDeployCallerPrefix = "autodeploy:"

// autoDeployer watches each opted-in project's GitHub branch and deploys
// new commits through the project's own wrapped updateLocalStack tool, so
// every deploy passes the same middleware chain (caller gate, compose
// policy, approval gating, stack scoping, env filter) as an agent's.
// It never creates a stack and never calls Portainer directly.
type autoDeployer struct {
	g        *Gateway
	gh       *githubClient
	interval time.Duration
	settle   time.Duration
	now      func() time.Time

	projects map[string]*adProject
	order    []string

	ctx    context.Context
	cancel context.CancelFunc
	stop   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// adProject is one watched project.
type adProject struct {
	cfg      ProjectConfig
	callerID string
	// nudge asks the project's goroutine to check now (cap 1).
	nudge chan struct{}

	mu        sync.Mutex
	pending   *pendingDeploy
	firstSeen map[string]time.Time
	// subjectSHA and subject cache the last commit subject fetched.
	subjectSHA, subject string
	// force deploys the current head once even if it is the baseline or
	// was rejected or failed before (the owner's "Deploy latest now").
	force    bool
	status   string
	statusAt time.Time
}

// pendingDeploy is a deploy waiting for the owner's approval.
type pendingDeploy struct {
	SHA, Subject string
	Args         map[string]any
	Origin       middleware.DeployOrigin
	Token        string
	RequestID    string
}

func newAutoDeployer(g *Gateway, gh *githubClient, interval time.Duration) *autoDeployer {
	ctx, cancel := context.WithCancel(context.Background())
	a := &autoDeployer{
		g:        g,
		gh:       gh,
		interval: interval,
		settle:   checksSettle,
		now:      time.Now,
		projects: map[string]*adProject{},
		ctx:      ctx,
		cancel:   cancel,
		stop:     make(chan struct{}),
	}
	for _, p := range g.cfg.Projects {
		if p.AutoDeploy == nil {
			continue
		}
		a.projects[p.Name] = &adProject{
			cfg:       p,
			callerID:  autoDeployCallerPrefix + p.Name,
			nudge:     make(chan struct{}, 1),
			firstSeen: map[string]time.Time{},
			status:    "starting",
		}
		a.order = append(a.order, p.Name)
	}
	return a
}

// start runs one goroutine per project.
func (a *autoDeployer) start() {
	for _, name := range a.order {
		a.wg.Add(1)
		go a.run(a.projects[name])
	}
}

// close stops the goroutines, waiting for a deploy in progress to finish.
func (a *autoDeployer) close() {
	a.once.Do(func() {
		close(a.stop)
		a.cancel()
	})
	a.wg.Wait()
}

func (a *autoDeployer) jittered() time.Duration {
	d := a.interval
	return d - d/10 + time.Duration(rand.Int64N(int64(d/5)+1))
}

func (a *autoDeployer) run(p *adProject) {
	defer a.wg.Done()
	// The first check soon after start sets the baseline; projects start
	// at random times within the first 10 seconds.
	first := min(a.interval, 10*time.Second)
	timer := time.NewTimer(time.Duration(rand.Int64N(int64(first) + 1)))
	defer timer.Stop()
	var last time.Time
	for {
		select {
		case <-a.stop:
			return
		case <-timer.C:
		case <-p.nudge:
			if gap := minNudgeGap - time.Since(last); gap > 0 {
				select {
				case <-a.stop:
					return
				case <-time.After(gap):
				}
			}
		}
		a.safeCheck(p)
		last = time.Now()
		timer.Reset(a.jittered())
	}
}

// safeCheck runs check, keeping the gateway up if it panics.
func (a *autoDeployer) safeCheck(p *adProject) {
	defer func() {
		if r := recover(); r != nil {
			a.event(p, "panic", "").with(sanitizeText(fmt.Sprint(r), maxErrorRunes)).log()
			p.setStatus(a.now(), "internal error; see the gateway log")
		}
	}()
	a.check(a.ctx, p)
}

// nudgeRepo asks every project watching repo's branch named by ref
// ("refs/heads/<branch>") to check now. It returns how many matched. A
// nudge carries no data: the watcher always asks GitHub itself.
func (a *autoDeployer) nudgeRepo(repo, ref string) int {
	if a == nil {
		return 0
	}
	n := 0
	for _, name := range a.order {
		p := a.projects[name]
		ad := p.cfg.AutoDeploy
		if strings.EqualFold(repo, ad.Repo) && ref == "refs/heads/"+ad.Branch {
			p.poke()
			n++
		}
	}
	return n
}

// nudgeProject asks one project to check now.
func (a *autoDeployer) nudgeProject(name string) {
	if a == nil {
		return
	}
	if p := a.projects[name]; p != nil {
		p.poke()
	}
}

func (p *adProject) poke() {
	select {
	case p.nudge <- struct{}{}:
	default:
	}
}

// forceLatest is the owner's "Deploy latest now": forget the rejected and
// failed commits and deploy the current head once, even if it is the
// baseline. Checks and approvals still apply.
func (a *autoDeployer) forceLatest(name string) error {
	if a == nil {
		return errors.New("auto-deploy is not enabled")
	}
	p := a.projects[name]
	if p == nil {
		return errors.New("auto-deploy is not enabled for that project")
	}
	if rec, ok := a.g.store.AutoDeployRecord(name); ok {
		rec.RejectedSHA, rec.FailedSHA, rec.LastError = "", "", ""
		if err := a.g.store.SetAutoDeployRecord(name, rec); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.force = true
	p.mu.Unlock()
	p.setStatus(a.now(), "deploy of the latest commit requested")
	p.poke()
	return nil
}

func (p *adProject) setStatus(now time.Time, s string) {
	p.mu.Lock()
	p.status, p.statusAt = s, now
	p.mu.Unlock()
}

func (p *adProject) takePending() *pendingDeploy {
	p.mu.Lock()
	defer p.mu.Unlock()
	pd := p.pending
	p.pending = nil
	return pd
}

func (p *adProject) setPending(pd *pendingDeploy) {
	p.mu.Lock()
	p.pending = pd
	p.mu.Unlock()
}

func (p *adProject) peekPending() *pendingDeploy {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
}

func (p *adProject) forced() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.force
}

func (p *adProject) clearForce() {
	p.mu.Lock()
	p.force = false
	p.mu.Unlock()
}

// event writes an audit event for a state transition.
func (a *autoDeployer) event(p *adProject, stage, sha string) *auditEvent {
	return &auditEvent{a: a, p: p, stage: stage, sha: sha}
}

type auditEvent struct {
	a          *autoDeployer
	p          *adProject
	stage, sha string
	detail     string
}

func (e *auditEvent) with(detail string) *auditEvent {
	e.detail = detail
	return e
}

func (e *auditEvent) log() {
	ev := e.a.g.audit.Info().Str("event", "autodeploy").Str("project", e.p.cfg.Name).
		Str("repo", e.p.cfg.AutoDeploy.Repo).Str("branch", e.p.cfg.AutoDeploy.Branch).Str("stage", e.stage)
	if e.sha != "" {
		ev = ev.Str("commit", e.sha)
	}
	if e.detail != "" {
		ev = ev.Str("detail", e.detail)
	}
	ev.Msg("")
}

func (a *autoDeployer) saveRecord(p *adProject, rec AutoDeployRecord) bool {
	if err := a.g.store.SetAutoDeployRecord(p.cfg.Name, rec); err != nil {
		a.event(p, "state_save_failed", "").with(sanitizeText(err.Error(), maxErrorRunes)).log()
		p.setStatus(a.now(), "failed to save auto-deploy state; nothing deployed")
		return false
	}
	return true
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// check runs one step of the watcher's state machine for p. Ticks and
// nudges call it from p's goroutine only, so steps never overlap.
func (a *autoDeployer) check(ctx context.Context, p *adProject) {
	ad := p.cfg.AutoDeploy
	name := p.cfg.Name
	fp := ad.Fingerprint()

	// 1. A deploy waiting for approval.
	if pd := p.peekPending(); pd != nil {
		status, err := a.g.approvals.Status(ctx, pd.RequestID)
		switch {
		case err != nil:
			// Unknown to the approval server (pruned): start over.
			p.takePending()
			a.event(p, "approval_lost", pd.SHA).log()
		case status == approval.StatusApproved:
			p.takePending()
			a.event(p, "approved", pd.SHA).log()
			args := maps.Clone(pd.Args)
			args["approvalToken"] = pd.Token
			res, err := a.g.mcp.callInternal(ctx, name, a.caller(p, &pd.Origin), "updateLocalStack", args)
			if _, still := middleware.ApprovalTokenFromResult(res); still && err == nil {
				// Not decided after all; keep waiting.
				p.setPending(pd)
				return
			}
			a.finish(p, pd.SHA, res, err)
			return
		case status == approval.StatusRejected:
			p.takePending()
			rec, _ := a.g.store.AutoDeployRecord(name)
			if rec.Fingerprint == fp {
				rec.RejectedSHA = pd.SHA
				a.saveRecord(p, rec)
			}
			a.event(p, "rejected", pd.SHA).log()
			p.setStatus(a.now(), fmt.Sprintf("%s was rejected (or its approval expired); waiting for a newer commit", short(pd.SHA)))
		}
	}

	// 2. The branch head.
	head, err := a.gh.branchHead(ctx, ad.Repo, ad.Branch)
	if err != nil {
		p.setStatus(a.now(), "cannot read the branch: "+sanitizeText(err.Error(), maxErrorRunes))
		return
	}
	p.mu.Lock()
	if _, ok := p.firstSeen[head]; !ok {
		p.firstSeen = map[string]time.Time{head: a.now()}
	}
	firstSeen := p.firstSeen[head]
	p.mu.Unlock()
	force := p.forced()

	// 3. Baseline: on first enable, or when the repo, branch or path
	// changed, the current head counts as deployed.
	rec, ok := a.g.store.AutoDeployRecord(name)
	if !ok || rec.Fingerprint != fp {
		rec = AutoDeployRecord{Fingerprint: fp, DeployedSHA: head}
		if !a.saveRecord(p, rec) {
			return
		}
		a.event(p, "baseline", head).log()
		if !force {
			p.setStatus(a.now(), fmt.Sprintf("watching; %s is the baseline (not deployed by towline)", short(head)))
			return
		}
	}

	// 4. Already waiting for this commit.
	if pd := p.peekPending(); pd != nil && pd.SHA == head {
		p.setStatus(a.now(), fmt.Sprintf("%s is waiting for your approval", short(head)))
		return
	}

	// 5. Nothing new. A request for another commit is withdrawn (the
	// branch moved back).
	if !force && (head == rec.DeployedSHA || head == rec.RejectedSHA || head == rec.FailedSHA) {
		if pd := p.takePending(); pd != nil {
			a.withdraw(p, pd)
		}
		switch head {
		case rec.DeployedSHA:
			p.setStatus(a.now(), fmt.Sprintf("up to date at %s", short(head)))
		case rec.RejectedSHA:
			p.setStatus(a.now(), fmt.Sprintf("%s was rejected; waiting for a newer commit", short(head)))
		default:
			p.setStatus(a.now(), fmt.Sprintf("%s failed to deploy; waiting for a newer commit", short(head)))
		}
		return
	}

	// A newer commit supersedes the one waiting for approval.
	if pd := p.takePending(); pd != nil {
		a.withdraw(p, pd)
	}

	// 6. Checks.
	checkSummary := "not required"
	if ad.ChecksRequired() {
		cr, err := a.gh.checks(ctx, ad.Repo, head)
		if err != nil {
			p.setStatus(a.now(), "cannot read checks: "+sanitizeText(err.Error(), maxErrorRunes))
			return
		}
		switch cr.State {
		case checksReady:
		case checksFailed:
			p.setStatus(a.now(), fmt.Sprintf("checks failed for %s: %s", short(head), cr.Summary))
			return
		default:
			p.setStatus(a.now(), fmt.Sprintf("waiting for checks on %s: %s", short(head), cr.Summary))
			return
		}
		if wait := a.settle - a.now().Sub(firstSeen); wait > 0 {
			p.setStatus(a.now(), fmt.Sprintf("checks passed for %s; waiting %s for late checks", short(head), wait.Round(time.Second)))
			return
		}
		checkSummary = cr.Summary
	}

	// 7. The compose file at that commit, and the existing stack.
	compose, err := a.gh.composeAt(ctx, ad.Repo, ad.ComposePath, head)
	if err != nil {
		p.setStatus(a.now(), "cannot read the compose file: "+sanitizeText(err.Error(), maxErrorRunes))
		return
	}
	subject := a.subject(ctx, p, head)
	stackID, envID, err := a.findStack(ctx, p)
	if err != nil {
		p.setStatus(a.now(), sanitizeText(err.Error(), maxErrorRunes))
		return
	}

	// 8. Deploy through the project's own updateLocalStack.
	origin := middleware.DeployOrigin{
		Summary: fmt.Sprintf("Auto-deploy %s@%s: %s", ad.Repo, short(head), subject),
		Details: fmt.Sprintf("Auto-deploy of a new commit on %s, branch %s\nCommit: %s\nSubject: %s\nCompose file: %s at that commit\nChecks: %s\nStack: %s",
			ad.Repo, ad.Branch, head, subject, ad.ComposePath, checkSummary, p.cfg.Stack),
		Commit:    head,
		CommitEnv: ad.CommitEnv,
	}
	// Numbers are float64, as they arrive from JSON in an agent's call
	// (the upstream handlers parse them that way).
	args := map[string]any{"id": float64(stackID), "environmentId": float64(envID), "file": compose, "pullImage": true}
	p.clearForce()
	res, err := a.g.mcp.callInternal(ctx, name, a.caller(p, &origin), "updateLocalStack", maps.Clone(args))
	if token, ok := middleware.ApprovalTokenFromResult(res); ok && err == nil {
		bp := a.g.mcp.built[name]
		var reqID string
		if bp != nil && bp.Approvals != nil {
			if pa := bp.Approvals.Peek(token, args); pa != nil {
				reqID = pa.RequestID
			}
		}
		if reqID == "" {
			a.finish(p, head, mcp.NewToolResultError("the approval request could not be tracked"), nil)
			return
		}
		p.setPending(&pendingDeploy{SHA: head, Subject: subject, Args: args, Origin: origin, Token: token, RequestID: reqID})
		a.event(p, "approval_requested", head).log()
		p.setStatus(a.now(), fmt.Sprintf("%s is waiting for your approval", short(head)))
		return
	}
	a.finish(p, head, res, err)
}

// finish records the outcome of a deploy attempt. A failure is not retried
// automatically (a retry would need a new approval and could loop on a
// compose file the policy refuses).
func (a *autoDeployer) finish(p *adProject, sha string, res *mcp.CallToolResult, callErr error) {
	p.clearForce()
	rec, _ := a.g.store.AutoDeployRecord(p.cfg.Name)
	rec.Fingerprint = p.cfg.AutoDeploy.Fingerprint()
	if callErr != nil || res == nil || res.IsError {
		msg := "deploy failed"
		if callErr != nil {
			msg = callErr.Error()
		} else if t := resultText(res); t != "" {
			msg = t
		}
		rec.FailedSHA = sha
		rec.LastError = sanitizeText(msg, maxErrorRunes)
		a.saveRecord(p, rec)
		a.event(p, "failed", sha).with(rec.LastError).log()
		p.setStatus(a.now(), fmt.Sprintf("%s failed to deploy", short(sha)))
		return
	}
	rec.DeployedSHA = sha
	rec.DeployedAt = a.now().UTC()
	rec.LastError, rec.FailedSHA = "", ""
	a.saveRecord(p, rec)
	a.event(p, "deployed", sha).log()
	p.setStatus(a.now(), fmt.Sprintf("deployed %s", short(sha)))
}

// withdraw marks a superseded approval request so it can't be approved.
func (a *autoDeployer) withdraw(p *adProject, pd *pendingDeploy) {
	_ = a.g.approvals.Withdraw(pd.RequestID)
	if bp := a.g.mcp.built[p.cfg.Name]; bp != nil && bp.Approvals != nil {
		bp.Approvals.Consume(pd.Token)
	}
	a.event(p, "superseded", pd.SHA).log()
}

func (a *autoDeployer) caller(p *adProject, origin *middleware.DeployOrigin) middleware.Caller {
	return middleware.Caller{ID: p.callerID, Scope: middleware.ScopeDeploy, Origin: origin}
}

// subject returns the sanitized subject of a commit (cached per head).
func (a *autoDeployer) subject(ctx context.Context, p *adProject, sha string) string {
	p.mu.Lock()
	if p.subjectSHA == sha {
		s := p.subject
		p.mu.Unlock()
		return s
	}
	p.mu.Unlock()
	raw, err := a.gh.commitSubject(ctx, p.cfg.AutoDeploy.Repo, sha)
	s := sanitizeLine(raw, maxSubjectRunes)
	if err != nil || s == "" {
		s = "(no commit message)"
	}
	if err == nil {
		p.mu.Lock()
		p.subjectSHA, p.subject = sha, s
		p.mu.Unlock()
	}
	return s
}

// findStack looks the project's stack up through its own listLocalStacks
// (scoped to the stack). Auto-deploy only updates an existing stack.
func (a *autoDeployer) findStack(ctx context.Context, p *adProject) (id, envID int, err error) {
	res, err := a.g.mcp.callInternal(ctx, p.cfg.Name, a.caller(p, nil), "listLocalStacks", map[string]any{})
	if err != nil || res == nil || res.IsError {
		return 0, 0, errors.New("failed to list stacks; auto-deploy skipped")
	}
	var stacks []struct {
		ID         int    `json:"id"`
		Name       string `json:"name"`
		EndpointID int    `json:"endpoint_id"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &stacks); err != nil {
		return 0, 0, errors.New("unexpected stack list; auto-deploy skipped")
	}
	for _, s := range stacks {
		if s.Name == p.cfg.Stack && s.ID > 0 && s.EndpointID > 0 {
			return s.ID, s.EndpointID, nil
		}
	}
	return 0, 0, fmt.Errorf("stack %s not found; auto-deploy skipped (it never creates stacks)", p.cfg.Stack)
}

func resultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// --- Internal tool calls ---

// internalTools are the only tools the watcher may call.
var internalTools = map[string]bool{"listLocalStacks": true, "updateLocalStack": true}

// callInternal runs a project's wrapped tool for the auto-deploy watcher,
// with the full middleware chain, and audits it like guard does. There is
// no connection, so guard's connection check doesn't apply; the caller is
// the watcher's own synthetic identity.
func (rt *mcpRouter) callInternal(ctx context.Context, project string, c middleware.Caller, tool string, args map[string]any) (res *mcp.CallToolResult, err error) {
	if !internalTools[tool] || !strings.HasPrefix(c.ID, autoDeployCallerPrefix) {
		return nil, fmt.Errorf("tool %s is not available to auto-deploy", tool)
	}
	bp := rt.built[project]
	if bp == nil {
		return nil, fmt.Errorf("unknown project %s", project)
	}
	st, ok := bp.Tools[tool]
	if !ok || st.Handler == nil {
		return nil, fmt.Errorf("project %s has no %s tool", project, tool)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	start := time.Now()
	func() {
		defer func() {
			if p := recover(); p != nil {
				res, err = nil, fmt.Errorf("tool %s failed: %v", tool, p)
			}
		}()
		res, err = st.Handler(middleware.WithCaller(ctx, c), req)
	}()
	repo := ""
	if p, ok := rt.g.cfg.Project(project); ok && p.AutoDeploy != nil {
		repo = p.AutoDeploy.Repo
	}
	ev := rt.g.audit.Info().Str("event", "tool_call").Str("connection", c.ID).Str("connection_name", "auto-deploy "+repo).
		Str("project", project).Str("tool", tool).Str("source", "auto-deploy").Dur("duration", time.Since(start))
	if c.Origin != nil {
		ev = ev.Str("commit", c.Origin.Commit)
	}
	switch {
	case err != nil:
		ev.Str("outcome", "error").Msg("")
	case res != nil && res.IsError:
		ev.Str("outcome", "refused_or_failed").Msg("")
	default:
		ev.Str("outcome", "ok").Msg("")
	}
	return res, err
}

// --- Owner UI view ---

// autoDeployView is a project's auto-deploy state for the owner page.
type autoDeployView struct {
	Project, Repo, Branch, Path string
	ChecksRequired              bool
	Approval                    string
	Deployed                    string
	DeployedAt                  time.Time
	State                       string
	LastError                   string
	Waiting                     bool
}

func (a *autoDeployer) views() []autoDeployView {
	if a == nil {
		return nil
	}
	var out []autoDeployView
	for _, name := range a.order {
		p := a.projects[name]
		ad := p.cfg.AutoDeploy
		v := autoDeployView{Project: name, Repo: ad.Repo, Branch: ad.Branch, Path: ad.ComposePath, ChecksRequired: ad.ChecksRequired(), Approval: p.cfg.Approval}
		if rec, ok := a.g.store.AutoDeployRecord(name); ok && rec.Fingerprint == ad.Fingerprint() {
			v.Deployed = short(rec.DeployedSHA)
			v.DeployedAt = rec.DeployedAt
			v.LastError = rec.LastError
		}
		p.mu.Lock()
		v.State = p.status
		v.Waiting = p.pending != nil
		p.mu.Unlock()
		out = append(out, v)
	}
	return out
}

// --- Sanitizing text from GitHub ---

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[@-_]`)

// sanitizeLine keeps the first line of s, without control, formatting
// (bidi, zero-width) or separator characters and ANSI escapes, cut to
// maxRunes runes.
func sanitizeLine(s string, maxRunes int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = ansiEscape.ReplaceAllString(s, "")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			continue
		}
		if n == maxRunes {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// sanitizeText is sanitizeLine for multi-line text, joined into one line.
func sanitizeText(s string, maxRunes int) string {
	return sanitizeLine(strings.Join(strings.Fields(s), " "), maxRunes)
}
