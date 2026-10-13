package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is a mutable stand-in for the GitHub REST API.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	heads    map[string]string           // "owner/repo@branch" -> sha
	files    map[string]string           // "sha:path" -> content
	runs     map[string][]map[string]any // sha -> check runs
	statuses map[string]string           // sha -> combined state ("" = none)
	messages map[string]string           // sha -> commit message
	repoIDs  map[string]int64            // repo -> id (default 42)
	requests []string                    // "path auth=<header>"
	override func(http.ResponseWriter, *http.Request) bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{heads: map[string]string{}, files: map[string]string{}, runs: map[string][]map[string]any{},
		statuses: map[string]string{}, messages: map[string]string{}, repoIDs: map[string]int64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) client(token string) *githubClient {
	return newGitHubClient(f.srv.URL, token)
}

func (f *fakeGitHub) setHead(repo, branch, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heads[repo+"@"+branch] = sha
}

// commit adds a commit with a compose file and green checks.
func (f *fakeGitHub) commit(sha, path, compose, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[sha+":"+path] = compose
	f.messages[sha] = message
	f.runs[sha] = []map[string]any{{"status": "completed", "conclusion": "success"}}
}

func (f *fakeGitHub) setRuns(sha string, runs ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[sha] = runs
}

func (f *fakeGitHub) count(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.EscapedPath()+" auth="+r.Header.Get("Authorization")+" inm="+r.Header.Get("If-None-Match"))
	override := f.override
	f.mu.Unlock()
	if override != nil && override(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-GitHub-Api-Version") == "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "towline-gateway/") {
		http.Error(w, "missing headers", http.StatusBadRequest)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/repos/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) == 2 {
		repo := parts[0] + "/" + parts[1]
		id, ok := f.repoIDs[repo]
		if !ok {
			id = 42
		}
		etag := fmt.Sprintf(`"repo-%d"`, id)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "full_name": repo})
		return
	}
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	repo, tail := parts[0]+"/"+parts[1], parts[2]
	switch {
	case strings.HasPrefix(tail, "commits/heads/"):
		sha, ok := f.heads[repo+"@"+strings.TrimPrefix(tail, "commits/heads/")]
		if !ok || r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.NotFound(w, r)
			return
		}
		etag := `"` + sha + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		fmt.Fprint(w, sha)
	case strings.HasPrefix(tail, "git/commits/"):
		sha := strings.TrimPrefix(tail, "git/commits/")
		msg, ok := f.messages[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha, "message": msg})
	case strings.HasSuffix(tail, "/check-runs"):
		sha := strings.TrimSuffix(strings.TrimPrefix(tail, "commits/"), "/check-runs")
		runs := f.runs[sha]
		if runs == nil {
			runs = []map[string]any{}
		}
		writeJSONWithETag(w, r, map[string]any{"total_count": len(runs), "check_runs": runs})
	case strings.HasSuffix(tail, "/status"):
		sha := strings.TrimSuffix(strings.TrimPrefix(tail, "commits/"), "/status")
		state := f.statuses[sha]
		n := 0
		if state != "" {
			n = 1
		} else {
			state = "pending" // GitHub's combined state with no statuses
		}
		writeJSONWithETag(w, r, map[string]any{"state": state, "total_count": n})
	case strings.HasPrefix(tail, "contents/"):
		content, ok := f.files[r.URL.Query().Get("ref")+":"+strings.TrimPrefix(tail, "contents/")]
		if !ok || r.Header.Get("Accept") != "application/vnd.github.raw" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, content)
	default:
		http.NotFound(w, r)
	}
}

// writeJSONWithETag answers 304 when the client already has this body.
func writeJSONWithETag(w http.ResponseWriter, r *http.Request, v any) {
	body, _ := json.Marshal(v)
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	_, _ = w.Write(body)
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccc"
)

func TestGitHubBranchHead(t *testing.T) {
	f := newFakeGitHub(t)
	f.setHead("acme/shop", "release/v1", shaA)
	gh := f.client("")
	ctx := context.Background()

	sha, err := gh.branchHead(ctx, "acme/shop", "release/v1")
	if err != nil || sha != shaA {
		t.Fatalf("head = %q, %v", sha, err)
	}
	// The second read is conditional and reuses the cached SHA.
	sha, err = gh.branchHead(ctx, "acme/shop", "release/v1")
	if err != nil || sha != shaA {
		t.Fatalf("cached head = %q, %v", sha, err)
	}
	if f.count("/repos/acme/shop/commits/heads/release/v1") != 2 {
		t.Fatalf("requests: %v", f.requests)
	}
	f.setHead("acme/shop", "release/v1", shaB)
	if sha, _ := gh.branchHead(ctx, "acme/shop", "release/v1"); sha != shaB {
		t.Fatalf("head after push = %q", sha)
	}

	// Anything that isn't a 40-hex SHA is refused.
	f.setHead("acme/shop", "main", "ABCDEF")
	if _, err := gh.branchHead(ctx, "acme/shop", "main"); err == nil {
		t.Fatal("non-SHA head accepted")
	}
	f.setHead("acme/shop", "main", strings.Repeat("A", 40))
	if _, err := gh.branchHead(ctx, "acme/shop", "main"); err == nil {
		t.Fatal("upper-case SHA accepted")
	}
	if _, err := gh.branchHead(ctx, "acme/missing", "main"); err == nil {
		t.Fatal("missing branch read")
	}
}

func TestGitHubRefusesRedirects(t *testing.T) {
	f := newFakeGitHub(t)
	f.override = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/acme/") {
			http.Redirect(w, r, "/repos/evil/shop"+strings.TrimPrefix(r.URL.Path, "/repos/acme/shop"), http.StatusMovedPermanently)
			return true
		}
		return false
	}
	f.setHead("evil/shop", "main", shaA)
	if _, err := f.client("").branchHead(context.Background(), "acme/shop", "main"); err == nil || !strings.Contains(err.Error(), "301") {
		t.Fatalf("redirect followed: %v", err)
	}
	if f.count("/repos/evil/") != 0 {
		t.Fatal("redirect target was requested")
	}
}

func TestGitHubRateLimitBackoff(t *testing.T) {
	f := newFakeGitHub(t)
	f.override = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Retry-After", "120")
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return true
	}
	gh := f.client("")
	now := time.Now()
	gh.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := gh.branchHead(ctx, "acme/shop", "main"); err == nil {
		t.Fatal("429 accepted")
	}
	if _, err := gh.branchHead(ctx, "acme/shop", "main"); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("no backoff: %v", err)
	}
	if n := f.count("/repos/"); n != 1 {
		t.Fatalf("requests during backoff: %d", n)
	}
	now = now.Add(121 * time.Second)
	_, _ = gh.branchHead(ctx, "acme/shop", "main")
	if n := f.count("/repos/"); n != 2 {
		t.Fatalf("no request after backoff: %d", n)
	}

	// X-RateLimit-Reset (403), capped at an hour.
	f.override = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(10*time.Hour).Unix()))
		http.Error(w, "rate limited", http.StatusForbidden)
		return true
	}
	now = now.Add(121 * time.Second)
	_, _ = gh.branchHead(ctx, "acme/shop", "main")
	if until := gh.backoffUntil.Sub(now); until > maxBackoff || until < 59*time.Minute {
		t.Fatalf("backoff %s", until)
	}
}

func TestGitHubComposeAt(t *testing.T) {
	f := newFakeGitHub(t)
	f.commit(shaA, "deploy/prod compose.yml", "services:\n  web:\n    image: nginx\n", "x")
	gh := f.client("")
	ctx := context.Background()
	got, err := gh.composeAt(ctx, "acme/shop", "deploy/prod compose.yml", shaA)
	if err != nil || !strings.Contains(got, "nginx") {
		t.Fatalf("compose = %q, %v", got, err)
	}
	// Each segment is escaped, the separators are not.
	if f.count("/contents/deploy/prod%20compose.yml") != 1 {
		t.Fatalf("path not escaped per segment: %v", f.requests)
	}
	if _, err := gh.composeAt(ctx, "acme/shop", "x.yml", "main"); err == nil {
		t.Fatal("non-SHA ref accepted")
	}

	f.commit(shaB, "big.yml", strings.Repeat("a", maxComposeBytes+1), "x")
	if _, err := gh.composeAt(ctx, "acme/shop", "big.yml", shaB); err == nil {
		t.Fatal("oversized compose accepted")
	}
	f.commit(shaC, "ok.yml", strings.Repeat("a", maxComposeBytes), "x")
	if _, err := gh.composeAt(ctx, "acme/shop", "ok.yml", shaC); err != nil {
		t.Fatalf("compose at the size limit refused: %v", err)
	}
	f.commit(shaC, "bin.yml", "services:\xff\xfe", "x")
	if _, err := gh.composeAt(ctx, "acme/shop", "bin.yml", shaC); err == nil {
		t.Fatal("non-UTF-8 compose accepted")
	}
}

func TestGitHubTokenHandling(t *testing.T) {
	f := newFakeGitHub(t)
	f.setHead("acme/shop", "main", shaA)
	ctx := context.Background()
	if _, err := f.client("").branchHead(ctx, "acme/shop", "main"); err != nil {
		t.Fatal(err)
	}
	if f.count("auth=Bearer") != 0 {
		t.Fatal("authorization sent without a token")
	}
	const token = "github_pat_SECRET123"
	gh := f.client(token)
	if _, err := gh.branchHead(ctx, "acme/shop", "main"); err != nil {
		t.Fatal(err)
	}
	if f.count("auth=Bearer "+token) != 1 {
		t.Fatalf("token not sent: %v", f.requests)
	}
	// Errors never carry the token.
	for _, fn := range []func() error{
		func() error { _, err := gh.branchHead(ctx, "acme/missing", "main"); return err },
		func() error { _, err := gh.composeAt(ctx, "acme/shop", "x.yml", shaA); return err },
		func() error {
			_, err := newGitHubClient("http://127.0.0.1:1", token).branchHead(ctx, "acme/shop", "main")
			return err
		},
	} {
		err := fn()
		if err == nil || strings.Contains(err.Error(), token) {
			t.Fatalf("error %v", err)
		}
	}
}

func TestGitHubChecks(t *testing.T) {
	run := func(status, conclusion string) map[string]any {
		return map[string]any{"status": status, "conclusion": conclusion}
	}
	many := make([]map[string]any, maxCheckRuns+1)
	for i := range many {
		many[i] = run("completed", "success")
	}
	cases := []struct {
		name   string
		runs   []map[string]any
		status string
		want   string
	}{
		{"all green", []map[string]any{run("completed", "success"), run("completed", "success")}, "", checksReady},
		{"neutral and skipped", []map[string]any{run("completed", "neutral"), run("completed", "skipped"), run("completed", "success")}, "", checksReady},
		{"in progress", []map[string]any{run("completed", "success"), run("in_progress", "")}, "", checksPending},
		{"queued", []map[string]any{run("queued", "")}, "", checksPending},
		{"failure", []map[string]any{run("completed", "success"), run("completed", "failure")}, "", checksFailed},
		{"cancelled", []map[string]any{run("completed", "cancelled")}, "", checksFailed},
		{"timed out", []map[string]any{run("completed", "timed_out")}, "", checksFailed},
		{"no checks at all", nil, "", checksPending},
		{"statuses only, green", nil, "success", checksReady},
		{"statuses pending", []map[string]any{run("completed", "success")}, "pending", checksPending},
		{"status failure", []map[string]any{run("completed", "success")}, "failure", checksFailed},
		{"status error", nil, "error", checksFailed},
		{"too many runs", many, "", checksFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGitHub(t)
			f.runs[shaA] = tc.runs
			f.statuses[shaA] = tc.status
			got, err := f.client("").checks(context.Background(), "acme/shop", shaA)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.want {
				t.Fatalf("state %s (%s), want %s", got.State, got.Summary, tc.want)
			}
		})
	}

	// A malformed response is an error, never "ready".
	f := newFakeGitHub(t)
	f.override = func(w http.ResponseWriter, r *http.Request) bool {
		fmt.Fprint(w, `{"check_runs": []}`)
		return true
	}
	if _, err := f.client("").checks(context.Background(), "acme/shop", shaA); err == nil {
		t.Fatal("response without total_count accepted")
	}
}

func TestSanitizeLine(t *testing.T) {
	cases := map[string]string{
		"fix: login\nsecond line": "fix: login",
		"\x1b[31mred\x1b[0m text": "red text",
		"bidi ‮evil‬ trick":       "bidi evil trick",
		"zero​width":              "zerowidth",
		"tab\tand\x00null":        "tabandnull",
		strings.Repeat("é", 130):  strings.Repeat("é", 120) + "…",
		"line separator":          "lineseparator",
	}
	for in, want := range cases {
		if got := sanitizeLine(in, maxSubjectRunes); got != want {
			t.Errorf("sanitizeLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitHubChecksUseETags(t *testing.T) {
	f := newFakeGitHub(t)
	f.commit(shaA, "x.yml", "services: {}\n", "x")
	gh := f.client("")
	ctx := context.Background()
	first, err := gh.checks(ctx, "acme/shop", shaA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gh.checks(ctx, "acme/shop", shaA)
	if err != nil || second != first {
		t.Fatalf("cached checks = %+v, %v", second, err)
	}
	if f.count("/check-runs auth= inm=\"") != 1 || f.count("/status auth= inm=\"") != 1 {
		t.Fatalf("second read was not conditional: %v", f.requests)
	}
	// A change is seen.
	f.setRuns(shaA, map[string]any{"status": "completed", "conclusion": "failure"})
	if got, _ := gh.checks(ctx, "acme/shop", shaA); got.State != checksFailed {
		t.Fatalf("state after change %s", got.State)
	}
}

func TestGitHubRepoID(t *testing.T) {
	f := newFakeGitHub(t)
	f.repoIDs["acme/shop"] = 7
	gh := f.client("")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if id, err := gh.repoID(ctx, "acme/shop"); err != nil || id != 7 {
			t.Fatalf("repoID = %d, %v", id, err)
		}
	}
	if f.count("/repos/acme/shop auth= inm=\"") != 1 {
		t.Fatalf("second read was not conditional: %v", f.requests)
	}
	f.override = func(w http.ResponseWriter, r *http.Request) bool {
		fmt.Fprint(w, `{"id": 9, "full_name": "evil/shop"}`)
		return true
	}
	if _, err := newGitHubClient(f.srv.URL, "").repoID(ctx, "acme/shop"); err == nil {
		t.Fatal("answer for another repository accepted")
	}
}
