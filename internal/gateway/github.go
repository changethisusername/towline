package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// githubAPI is the only GitHub endpoint the gateway talks to.
const githubAPI = "https://api.github.com"

const (
	githubTimeout = 20 * time.Second
	// maxGitHubJSON bounds JSON responses.
	maxGitHubJSON = 1 << 20
	// maxComposeBytes bounds a compose file fetched from GitHub.
	maxComposeBytes = 512 << 10
	// maxCheckRuns is the most check runs one page returns; a commit with
	// more is refused rather than half-checked.
	maxCheckRuns = 100
	// maxBackoff caps how long a rate limit pauses requests.
	maxBackoff = time.Hour
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Check states.
const (
	checksReady   = "ready"
	checksPending = "pending"
	checksFailed  = "failed"
)

// checkResult is a commit's combined checks.
type checkResult struct {
	State   string
	Summary string
}

// githubClient reads branch heads, checks and files from the GitHub REST
// API. Every response is untrusted: sizes are capped, SHAs validated, and
// redirects (renamed or transferred repositories included) refused.
type githubClient struct {
	base  string
	token string
	hc    *http.Client
	now   func() time.Time

	mu           sync.Mutex
	etags        map[string]etagEntry
	backoffUntil time.Time
}

type etagEntry struct {
	etag string
	body string
}

// errGitHubStatus is an unexpected HTTP status from GitHub.
type errGitHubStatus struct {
	Code int
	Path string
}

func (e *errGitHubStatus) Error() string {
	return fmt.Sprintf("github: %s: HTTP %d", e.Path, e.Code)
}

func newGitHubClient(base, token string) *githubClient {
	return &githubClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		hc: &http.Client{
			Timeout: githubTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:   time.Now,
		etags: map[string]etagEntry{},
	}
}

// repoPath is /repos/<owner>/<name> with each part escaped. The config
// already restricts both to safe characters.
func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// escapeSegments escapes each segment of a slash-separated path.
func escapeSegments(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// get performs one GET. It returns the body (at most limit bytes; more is
// an error) and the status, which is 200 or 304 (with an ETag) only.
func (c *githubClient) get(ctx context.Context, path, accept string, limit int64, useETag bool) ([]byte, error) {
	c.mu.Lock()
	if wait := c.backoffUntil.Sub(c.now()); wait > 0 {
		c.mu.Unlock()
		return nil, fmt.Errorf("github: rate limited for another %s", wait.Round(time.Second))
	}
	cached, hasCached := c.etags[path]
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("github: %s: invalid request", pathOnly(path))
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "towline-gateway/"+Version)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if useETag && hasCached {
		req.Header.Set("If-None-Match", cached.etag)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// Transport errors can quote the URL but never headers; the
		// token travels only in a header.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("github: %s: %v", pathOnly(path), err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotModified && useETag && hasCached:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return []byte(cached.body), nil
	default:
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			c.noteRateLimit(resp.Header)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &errGitHubStatus{Code: resp.StatusCode, Path: pathOnly(path)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("github: %s: failed to read response", pathOnly(path))
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("github: %s: response larger than %d bytes", pathOnly(path), limit)
	}
	if useETag {
		if etag := resp.Header.Get("ETag"); etag != "" && len(etag) <= 200 {
			c.mu.Lock()
			if len(c.etags) > 256 {
				c.etags = map[string]etagEntry{}
			}
			c.etags[path] = etagEntry{etag: etag, body: string(body)}
			c.mu.Unlock()
		}
	}
	return body, nil
}

// noteRateLimit pauses requests as GitHub asks.
func (c *githubClient) noteRateLimit(h http.Header) {
	now := c.now()
	var until time.Time
	if s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && s > 0 {
		until = now.Add(time.Duration(s) * time.Second)
	} else if h.Get("X-RateLimit-Remaining") == "0" {
		if r, err := strconv.ParseInt(strings.TrimSpace(h.Get("X-RateLimit-Reset")), 10, 64); err == nil {
			until = time.Unix(r, 0)
		}
	}
	if until.IsZero() || !until.After(now) {
		return
	}
	if until.Sub(now) > maxBackoff {
		until = now.Add(maxBackoff)
	}
	c.mu.Lock()
	if until.After(c.backoffUntil) {
		c.backoffUntil = until
	}
	c.mu.Unlock()
}

// pathOnly drops the query string from an API path for error messages.
func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

// repoID returns the repository's numeric id, which stays the same across
// renames and differs for a repository deleted and recreated under the same
// name.
func (c *githubClient) repoID(ctx context.Context, repo string) (int64, error) {
	body, err := c.get(ctx, repoPath(repo), "application/vnd.github+json", maxGitHubJSON, true)
	if err != nil {
		return 0, err
	}
	var r struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.ID <= 0 {
		return 0, errors.New("github: invalid repository response")
	}
	if !strings.EqualFold(r.FullName, repo) {
		return 0, fmt.Errorf("github: %s answered for another repository", repoPath(repo))
	}
	return r.ID, nil
}

// branchHead returns the SHA the branch points at.
func (c *githubClient) branchHead(ctx context.Context, repo, branch string) (string, error) {
	path := repoPath(repo) + "/commits/heads/" + escapeSegments(branch)
	body, err := c.get(ctx, path, "application/vnd.github.sha", 128, true)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(body))
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("github: %s: not a commit SHA", path)
	}
	return sha, nil
}

// commitSubject returns the commit message's raw first line. Callers
// sanitize it before showing it anywhere.
func (c *githubClient) commitSubject(ctx context.Context, repo, sha string) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", errors.New("github: invalid commit SHA")
	}
	body, err := c.get(ctx, repoPath(repo)+"/git/commits/"+sha, "application/vnd.github+json", maxGitHubJSON, false)
	if err != nil {
		return "", err
	}
	var commit struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return "", errors.New("github: invalid commit response")
	}
	if commit.SHA != sha {
		return "", errors.New("github: commit response is for another commit")
	}
	subject, _, _ := strings.Cut(commit.Message, "\n")
	return subject, nil
}

// checks combines a commit's check runs and commit statuses. It fails
// closed: a commit with no checks at all is pending, never ready.
func (c *githubClient) checks(ctx context.Context, repo, sha string) (checkResult, error) {
	if !shaPattern.MatchString(sha) {
		return checkResult{}, errors.New("github: invalid commit SHA")
	}
	// Both check reads are conditional, so a head that waits on its
	// checks for a long time costs little rate limit.
	body, err := c.get(ctx, repoPath(repo)+"/commits/"+sha+"/check-runs?per_page=100", "application/vnd.github+json", maxGitHubJSON, true)
	if err != nil {
		return checkResult{}, err
	}
	var runs struct {
		TotalCount *int `json:"total_count"`
		CheckRuns  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(body, &runs); err != nil || runs.TotalCount == nil {
		return checkResult{}, errors.New("github: invalid check runs response")
	}
	body, err = c.get(ctx, repoPath(repo)+"/commits/"+sha+"/status", "application/vnd.github+json", maxGitHubJSON, true)
	if err != nil {
		return checkResult{}, err
	}
	var status struct {
		State      string `json:"state"`
		TotalCount *int   `json:"total_count"`
	}
	if err := json.Unmarshal(body, &status); err != nil || status.TotalCount == nil {
		return checkResult{}, errors.New("github: invalid commit status response")
	}
	return combineChecks(*runs.TotalCount, len(runs.CheckRuns), func(i int) (string, string) {
		return runs.CheckRuns[i].Status, runs.CheckRuns[i].Conclusion
	}, *status.TotalCount, status.State), nil
}

func combineChecks(totalRuns, listed int, run func(int) (status, conclusion string), statuses int, state string) checkResult {
	if totalRuns > maxCheckRuns || listed > maxCheckRuns || listed != totalRuns {
		return checkResult{State: checksFailed, Summary: fmt.Sprintf("%d check runs (more than towline can check)", totalRuns)}
	}
	if totalRuns == 0 && statuses == 0 {
		return checkResult{State: checksPending, Summary: "no checks reported yet"}
	}
	pending, failed := 0, 0
	for i := 0; i < listed; i++ {
		st, concl := run(i)
		if st != "completed" {
			pending++
			continue
		}
		switch concl {
		case "success", "neutral", "skipped":
		default:
			failed++
		}
	}
	statusText := ""
	statusState := checksReady
	if statuses > 0 {
		switch state {
		case "success":
		case "pending":
			statusState = checksPending
		default:
			statusState = checksFailed
		}
		statusText = fmt.Sprintf(", %d commit statuses %s", statuses, sanitizeLine(state, 20))
	}
	summary := fmt.Sprintf("%d check runs (%d pending, %d failed)%s", totalRuns, pending, failed, statusText)
	switch {
	case failed > 0 || statusState == checksFailed:
		return checkResult{State: checksFailed, Summary: summary}
	case pending > 0 || statusState == checksPending:
		return checkResult{State: checksPending, Summary: summary}
	}
	return checkResult{State: checksReady, Summary: summary}
}

// composeAt returns the compose file at a commit.
func (c *githubClient) composeAt(ctx context.Context, repo, path, sha string) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", errors.New("github: invalid commit SHA")
	}
	p := repoPath(repo) + "/contents/" + escapeSegments(path) + "?ref=" + sha
	body, err := c.get(ctx, p, "application/vnd.github.raw", maxComposeBytes, false)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("github: %s is not UTF-8 text", path)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", fmt.Errorf("github: %s is empty", path)
	}
	return string(body), nil
}
