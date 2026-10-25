package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

const testHookSecret = "hook-secret-0123456789abcdef0123456789"

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (h *adHarness) hook(event, body, signature string) *httptest.ResponseRecorder {
	return h.hookFrom("203.0.113.7", event, body, signature)
}

func (h *adHarness) hookFrom(ip, event, body, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://127.0.0.1:9/hooks/github", strings.NewReader(body))
	req.Host = "127.0.0.1:9"
	req.RemoteAddr = ip + ":4444"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", "72d3162e-cc78-11e3-81ab-4c9367dc0958")
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	return rr
}

func (h *adHarness) nudged() bool {
	select {
	case <-h.autodeploy.projects["alpha"].nudge:
		return true
	default:
		return false
	}
}

func TestGitHubWebhook(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	push := func(repo, ref string) string {
		return `{"ref":"` + ref + `","after":"` + shaC + `","repository":{"full_name":"` + repo + `"}}`
	}

	// Off until a secret is configured.
	body := push("acme/shop", "refs/heads/main")
	if rr := h.hook("push", body, sign(testHookSecret, body)); rr.Code != 404 {
		t.Fatalf("no secret: %d", rr.Code)
	}
	h.cfg.GitHub.WebhookSecret = testHookSecret

	cases := []struct {
		name      string
		event     string
		body      string
		signature string
		code      int
		nudge     bool
	}{
		{"push to the branch", "push", body, sign(testHookSecret, body), 202, true},
		{"repo name in other case", "push", push("ACME/Shop", "refs/heads/main"), "", 202, true},
		{"other branch", "push", push("acme/shop", "refs/heads/dev"), "", 202, false},
		{"tag with the branch's name", "push", push("acme/shop", "refs/tags/main"), "", 202, false},
		{"other repo", "push", push("evil/shop", "refs/heads/main"), "", 202, false},
		{"ping", "ping", `{"zen":"x"}`, "", 200, false},
		{"other event", "issues", `{}`, "", 202, false},
		{"push without ref", "push", `{"repository":{"full_name":"acme/shop"}}`, "", 202, false},
		{"check suite completed", "check_suite", `{"action":"completed","repository":{"full_name":"acme/shop"}}`, "", 202, true},
		{"check suite on another branch", "check_suite", `{"action":"completed","check_suite":{"head_branch":"feature"},"repository":{"full_name":"acme/shop"}}`, "", 202, true},
		{"check suite requested", "check_suite", `{"action":"requested","repository":{"full_name":"acme/shop"}}`, "", 202, false},
		{"check suite other repo", "check_suite", `{"action":"completed","repository":{"full_name":"evil/shop"}}`, "", 202, false},
		{"status", "status", `{"state":"success","sha":"` + shaC + `","repository":{"full_name":"acme/shop"}}`, "", 202, true},
		{"status other repo", "status", `{"state":"success","repository":{"full_name":"evil/shop"}}`, "", 202, false},
		{"bad signature", "push", body, sign("wrong-secret-0123456789abcdef0123", body), 401, false},
		{"missing signature", "push", body, "-", 401, false},
		{"sha1 signature", "push", body, "sha1=" + strings.Repeat("0", 40), 401, false},
		{"oversized", "push", strings.Repeat(" ", maxHookBody+1), "", 413, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := tc.signature
			switch sig {
			case "":
				sig = sign(testHookSecret, tc.body)
			case "-":
				sig = ""
			}
			rr := h.hook(tc.event, tc.body, sig)
			if rr.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", rr.Code, tc.code, rr.Body.String())
			}
			if got := h.nudged(); got != tc.nudge {
				t.Fatalf("nudged = %v, want %v", got, tc.nudge)
			}
		})
	}
	audit := h.auditFn()
	if !strings.Contains(audit, `"event":"github_hook"`) || strings.Contains(audit, shaC) || strings.Contains(audit, "full_name") {
		t.Fatalf("audit:\n%s", audit)
	}

	// A nudge never deploys what the payload names: GitHub still says A.
	h.tick(t)
	if h.hook("push", body, sign(testHookSecret, body)).Code != 202 {
		t.Fatal("push refused")
	}
	h.tick(t)
	if len(h.cap.list()) != 0 {
		t.Fatal("deployed from webhook payload")
	}
}

func TestGitHubWebhookRateLimit(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.cfg.GitHub.WebhookSecret = testHookSecret
	body := `{"zen":"x"}`
	for i := 0; i < limitHookPerIP; i++ {
		if rr := h.hook("ping", body, sign(testHookSecret, body)); rr.Code != 200 {
			t.Fatalf("delivery %d: %d", i, rr.Code)
		}
	}
	if rr := h.hook("ping", body, sign(testHookSecret, body)); rr.Code != 429 {
		t.Fatalf("over the limit: %d", rr.Code)
	}
}

func TestGitHubWebhookWithoutAutoDeploy(t *testing.T) {
	tg := newTestGateway(t)
	tg.cfg.GitHub.WebhookSecret = testHookSecret
	body := `{"ref":"refs/heads/main","repository":{"full_name":"acme/shop"}}`
	req := httptest.NewRequest("POST", tg.url+"/hooks/github", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sign(testHookSecret, body))
	rr := httptest.NewRecorder()
	tg.Handler().ServeHTTP(rr, req)
	if rr.Code != 202 || !strings.Contains(rr.Body.String(), `"matched":0`) {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

// Unsigned deliveries only use up their own address's bucket, never the
// global one GitHub's signed deliveries need.
func TestGitHubWebhookUnsignedDontExhaustGlobalLimit(t *testing.T) {
	h := newAutoDeployHarness(t, startedRepo(t), t.TempDir(), ApprovalProd, nil)
	h.cfg.GitHub.WebhookSecret = testHookSecret
	body := `{"zen":"x"}`
	for ip := 0; ip < 6; ip++ {
		for i := 0; i < limitHookPerIP; i++ {
			h.hookFrom(fmt.Sprintf("198.51.100.%d", ip), "ping", body, "sha256=00")
		}
	}
	if rr := h.hookFrom("192.0.2.1", "ping", body, sign(testHookSecret, body)); rr.Code != 200 {
		t.Fatalf("signed delivery after %d unsigned ones: %d", 6*limitHookPerIP, rr.Code)
	}
}
