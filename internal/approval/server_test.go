package approval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWebhookToken  = "webhook-secret"
	testApproverToken = "approver-secret"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, err := NewServer(ServerConfig{WebhookToken: testWebhookToken, ApproverTokenHash: HashToken(testApproverToken)})
	require.NoError(t, err)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, method, url, token string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestNewServer_Validation(t *testing.T) {
	_, err := NewServer(ServerConfig{ApproverTokenHash: HashToken("x")})
	assert.Error(t, err)
	_, err = NewServer(ServerConfig{WebhookToken: "w"})
	assert.Error(t, err)
}

// TestServer_WebhookRoundTrip drives the server through the same client
// towline-mcp uses.
func TestServer_WebhookRoundTrip(t *testing.T) {
	_, ts := newTestServer(t)
	wh, err := NewWebhook(ts.URL, testWebhookToken)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, wh.Submit(ctx, Request{ID: "req1", Project: "app-prod", Action: "updateLocalStack", StackContent: "services: {}"}))
	status, err := wh.Status(ctx, "req1")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, status)

	// Duplicate IDs are refused.
	assert.Error(t, wh.Submit(ctx, Request{ID: "req1", Action: "x"}))

	// The webhook token cannot approve.
	resp := do(t, "POST", ts.URL+"/req1/approve", testWebhookToken, "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = do(t, "POST", ts.URL+"/req1/approve", testApproverToken, "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	status, err = wh.Status(ctx, "req1")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, status)

	// Decisions are final.
	resp = do(t, "POST", ts.URL+"/req1/reject", testApproverToken, "")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestServer_AuthRequired(t *testing.T) {
	_, ts := newTestServer(t)

	resp := do(t, "POST", ts.URL+"/", "", `{"id":"a","action":"x"}`)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp = do(t, "POST", ts.URL+"/", "wrong", `{"id":"a","action":"x"}`)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = do(t, "POST", ts.URL+"/", testWebhookToken, `{"id":"a","action":"x"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = do(t, "GET", ts.URL+"/a", "", "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp = do(t, "GET", ts.URL+"/api/requests", testWebhookToken, "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp = do(t, "POST", ts.URL+"/a/reject", "", "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = do(t, "GET", ts.URL+"/api/requests", testApproverToken, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []Record
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list, 1)
	assert.Equal(t, StatusPending, list[0].Status)
}

func TestServer_ExpiredRequestsAreRejected(t *testing.T) {
	s, ts := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }

	resp := do(t, "POST", ts.URL+"/", testWebhookToken, `{"id":"old","action":"x"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	now = now.Add(TokenTTL + time.Minute)
	resp = do(t, "POST", ts.URL+"/old/approve", testApproverToken, "")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	wh, _ := NewWebhook(ts.URL, testWebhookToken)
	status, err := wh.Status(context.Background(), "old")
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, status)
}

func TestServer_UIFlow(t *testing.T) {
	_, ts := newTestServer(t)
	resp := do(t, "POST", ts.URL+"/", testWebhookToken, `{"id":"ui1","project":"app-prod","action":"deleteLocalStack","description":"<script>x</script>"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Not logged in: login form, no request details.
	resp, err := client.Get(ts.URL + "/ui")
	require.NoError(t, err)
	page := readBody(t, resp)
	assert.Contains(t, page, "approver token")
	assert.NotContains(t, page, "deleteLocalStack")

	// Wrong token.
	resp, err = client.PostForm(ts.URL+"/ui/login", url.Values{"token": {"nope"}})
	require.NoError(t, err)
	assert.Contains(t, readBody(t, resp), "Invalid approver token")

	// Login.
	resp, err = client.PostForm(ts.URL+"/ui/login", url.Values{"token": {testApproverToken}})
	require.NoError(t, err)
	page = readBody(t, resp)
	assert.Contains(t, page, "deleteLocalStack")
	assert.NotContains(t, page, "<script>x</script>", "request content must be escaped")
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	require.NotNil(t, csrf)

	// Missing CSRF token is refused.
	resp, err = client.PostForm(ts.URL+"/ui/decide", url.Values{"id": {"ui1"}, "decision": {"approve"}})
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Cross-site post is refused even with the token.
	req, _ := http.NewRequest("POST", ts.URL+"/ui/decide", strings.NewReader(url.Values{"id": {"ui1"}, "decision": {"approve"}, "csrf": {csrf[1]}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, err = client.PostForm(ts.URL+"/ui/decide", url.Values{"id": {"ui1"}, "decision": {"reject"}, "csrf": {csrf[1]}})
	require.NoError(t, err)
	assert.Contains(t, readBody(t, resp), "rejected")

	wh, _ := NewWebhook(ts.URL, testWebhookToken)
	status, err := wh.Status(context.Background(), "ui1")
	require.NoError(t, err)
	assert.Equal(t, StatusRejected, status)
}

func TestServer_Ntfy(t *testing.T) {
	got := make(chan *http.Request, 1)
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r
	}))
	defer ntfy.Close()

	s, err := NewServer(ServerConfig{WebhookToken: testWebhookToken, ApproverTokenHash: HashToken(testApproverToken), NtfyURL: ntfy.URL, PublicURL: "https://approve.example/"})
	require.NoError(t, err)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := do(t, "POST", ts.URL+"/", testWebhookToken, `{"id":"n1","project":"app-prod","action":"towline_exec"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	select {
	case r := <-got:
		assert.Contains(t, r.Header.Get("Title"), "app-prod")
		assert.Equal(t, "https://approve.example/ui", r.Header.Get("Click"))
	case <-time.After(5 * time.Second):
		t.Fatal("no ntfy notification")
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
