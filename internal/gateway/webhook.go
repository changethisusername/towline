package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// maxHookBody bounds a webhook delivery.
	maxHookBody = 1 << 20
	// limitHookPerIP and limitHookGlobal bound webhook deliveries per
	// minute.
	limitHookPerIP  = 120
	limitHookGlobal = 600
)

var deliveryIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// handleGitHubHook receives GitHub push webhooks. A valid delivery only
// nudges the auto-deploy watcher to look at GitHub now: nothing in the
// payload (not even the pushed SHA) is used to decide what to deploy, so
// the webhook carries no authority.
func (g *Gateway) handleGitHubHook(w http.ResponseWriter, r *http.Request) {
	secret := g.cfg.GitHub.WebhookSecret
	if secret == "" {
		http.NotFound(w, r)
		return
	}
	ip := g.clientIP(r)
	if !g.limiter.allow("hook:"+ip, limitHookPerIP, time.Minute) || !g.limiter.allow("hook", limitHookGlobal, time.Minute) {
		tooMany(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) > maxHookBody {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !validHookSignature(secret, r.Header.Get("X-Hub-Signature-256"), body) {
		if !g.limiter.allow("authfail:"+ip, limitAuthFailPerIP, time.Minute) {
			tooMany(w)
			return
		}
		g.audit.Warn().Str("event", "github_hook").Str("outcome", "bad_signature").Str("ip", ip).Msg("")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")
	if !deliveryIDPattern.MatchString(delivery) {
		delivery = ""
	}
	ev := g.audit.Info().Str("event", "github_hook").Str("delivery", delivery)
	switch event {
	case "ping":
		ev.Str("type", "ping").Msg("")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	case "push":
		var push struct {
			Ref        string `json:"ref"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &push); err != nil {
			ev.Str("type", "push").Str("outcome", "invalid_payload").Msg("")
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		matched := g.autodeploy.nudgeRepo(push.Repository.FullName, push.Ref)
		ev.Str("type", "push").Int("matched", matched).Msg("")
		writeJSON(w, http.StatusAccepted, map[string]int{"matched": matched})
		return
	}
	ev.Str("type", "other").Msg("")
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
}

// validHookSignature checks GitHub's X-Hub-Signature-256 header.
func validHookSignature(secret, header string, body []byte) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
