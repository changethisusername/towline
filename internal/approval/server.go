package approval

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// maxRequestBody bounds approval request payloads (compose files included).
	maxRequestBody = 1 << 20
	// recordRetention is how long decided or expired requests stay visible.
	recordRetention = 24 * time.Hour
	// sessionTTL bounds how long a UI login lasts.
	sessionTTL    = 12 * time.Hour
	sessionCookie = "towline_approver"
)

// StatusExpired is shown in the UI for requests nobody decided on within
// TokenTTL. The webhook API reports them as rejected so the MCP stops.
const StatusExpired Status = "expired"

// ServerConfig configures the approval server.
type ServerConfig struct {
	// WebhookToken authenticates towline-mcp (submit requests, poll status).
	// It is visible to the agent, so it can never approve or reject.
	WebhookToken string
	// ApproverTokenHash is the hex SHA-256 of the human approver token.
	ApproverTokenHash string
	// NtfyURL, if set, receives a push notification for each new request
	// (e.g. https://ntfy.sh/my-private-topic).
	NtfyURL string
	// PublicURL is the address humans use to reach the UI, used in
	// notification links. Defaults to none.
	PublicURL string
}

// Record is an approval request and its decision.
type Record struct {
	Request
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	DecidedAt time.Time `json:"decidedAt,omitzero"`
}

// Server implements the approval webhook contract plus a small web UI and
// JSON API for human approvers.
type Server struct {
	cfg      ServerConfig
	mu       sync.Mutex
	records  map[string]*Record
	sessions map[string]session
	now      func() time.Time
	client   *http.Client
}

type session struct {
	csrf    string
	expires time.Time
}

// HashToken returns the hex SHA-256 of a token, as stored in config.
// Tokens are random 256-bit values, so a fast hash is sufficient.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken returns a random 256-bit token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// NewServer validates the configuration and creates a Server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.WebhookToken == "" {
		return nil, errors.New("webhook token is required")
	}
	if len(cfg.ApproverTokenHash) != sha256.Size*2 {
		return nil, errors.New("approver token hash is missing or invalid; run 'towline approvals setup'")
	}
	return &Server{
		cfg:      cfg,
		records:  map[string]*Record{},
		sessions: map[string]session{},
		now:      time.Now,
		client:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Webhook contract (towline-mcp)
	mux.HandleFunc("POST /{$}", s.handleSubmit)
	mux.HandleFunc("GET /{id}", s.handleStatus)

	// Approver API (CLI), bearer approver token
	mux.HandleFunc("GET /api/requests", s.requireApprover(s.handleList))
	mux.HandleFunc("POST /{id}/approve", s.requireApprover(s.decide(StatusApproved)))
	mux.HandleFunc("POST /{id}/reject", s.requireApprover(s.decide(StatusRejected)))

	// Web UI, session cookie + CSRF token
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /ui", s.handleUI)
	mux.HandleFunc("POST /ui/login", s.handleLogin)
	mux.HandleFunc("POST /ui/logout", s.handleLogout)
	mux.HandleFunc("POST /ui/decide", s.handleUIDecide)

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// --- Authentication ---

func bearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return t
	}
	return ""
}

func (s *Server) isWebhook(r *http.Request) bool {
	t := bearer(r)
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.cfg.WebhookToken)) == 1
}

func (s *Server) isApproverToken(token string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(s.cfg.ApproverTokenHash)) == 1
}

func (s *Server) requireApprover(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isApproverToken(bearer(r)) {
			writeError(w, http.StatusUnauthorized, "approver token required")
			return
		}
		next(w, r)
	}
}

// sessionFor returns the logged-in UI session, if any.
func (s *Server) sessionFor(r *http.Request) (string, session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[c.Value]
	if !ok || s.now().After(sess.expires) {
		delete(s.sessions, c.Value)
		return "", session{}, false
	}
	return c.Value, sess, true
}

// --- Webhook contract ---

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.isWebhook(r) {
		writeError(w, http.StatusUnauthorized, "webhook token required")
		return
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" || req.Action == "" || len(req.ID) > 128 {
		writeError(w, http.StatusBadRequest, "id and action are required")
		return
	}

	s.mu.Lock()
	s.pruneLocked()
	if _, exists := s.records[req.ID]; exists {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "request id already exists")
		return
	}
	rec := &Record{Request: req, Status: StatusPending, CreatedAt: s.now()}
	s.records[req.ID] = rec
	s.mu.Unlock()

	log.Printf("approval requested: %s on %s (id %s)", req.Action, req.Project, shortID(req.ID))
	if s.cfg.NtfyURL != "" {
		go s.notify(req)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": string(StatusPending)})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.isWebhook(r) && !s.isApproverToken(bearer(r)) {
		writeError(w, http.StatusUnauthorized, "token required")
		return
	}
	s.mu.Lock()
	s.pruneLocked()
	rec, ok := s.records[r.PathValue("id")]
	var status Status
	if ok {
		status = s.effectiveStatusLocked(rec)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "unknown request")
		return
	}
	if status == StatusExpired {
		status = StatusRejected
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(status)})
}

// --- Approver API ---

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot())
}

func (s *Server) decide(status Status) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.setDecision(r.PathValue("id"), status); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": string(status)})
	}
}

// setDecision records a human decision on a pending request.
func (s *Server) setDecision(id string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return errors.New("unknown request")
	}
	if current := s.effectiveStatusLocked(rec); current != StatusPending {
		return fmt.Errorf("request is already %s", current)
	}
	rec.Status = status
	rec.DecidedAt = s.now()
	log.Printf("approval %s: %s on %s (id %s)", status, rec.Action, rec.Project, shortID(id))
	return nil
}

func (s *Server) effectiveStatusLocked(rec *Record) Status {
	if rec.Status == StatusPending && s.now().Sub(rec.CreatedAt) > TokenTTL {
		return StatusExpired
	}
	return rec.Status
}

func (s *Server) pruneLocked() {
	cutoff := s.now().Add(-recordRetention)
	for id, rec := range s.records {
		if rec.CreatedAt.Before(cutoff) {
			delete(s.records, id)
		}
	}
}

// snapshot returns all records, newest first, with effective statuses.
func (s *Server) snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	out := make([]Record, 0, len(s.records))
	for _, rec := range s.records {
		c := *rec
		c.Status = s.effectiveStatusLocked(rec)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// --- Notifications ---

func (s *Server) notify(req Request) {
	body := fmt.Sprintf("%s on %s\n%s", req.Action, req.Project, req.Description)
	httpReq, err := http.NewRequest(http.MethodPost, s.cfg.NtfyURL, strings.NewReader(body))
	if err != nil {
		log.Printf("ntfy: %v", err)
		return
	}
	httpReq.Header.Set("Title", "Towline approval needed: "+req.Project)
	httpReq.Header.Set("Tags", "warning")
	if s.cfg.PublicURL != "" {
		httpReq.Header.Set("Click", strings.TrimRight(s.cfg.PublicURL, "/")+"/ui")
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("ntfy: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("ntfy: status %d", resp.StatusCode)
	}
}

// --- Web UI ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	if !s.isApproverToken(strings.TrimSpace(r.FormValue("token"))) {
		s.renderUI(w, "", "Invalid approver token.")
		return
	}
	id, csrf := NewToken(), NewToken()
	s.mu.Lock()
	s.sessions[id] = session{csrf: csrf, expires: s.now().Add(sessionTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if id, sess, ok := s.sessionFor(r); ok && sameOrigin(r) && r.FormValue("csrf") == sess.csrf {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func (s *Server) handleUIDecide(w http.ResponseWriter, r *http.Request) {
	_, sess, ok := s.sessionFor(r)
	if !ok {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
		return
	}
	if !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(sess.csrf)) != 1 {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	status := StatusRejected
	if r.FormValue("decision") == "approve" {
		status = StatusApproved
	}
	msg := ""
	if err := s.setDecision(r.FormValue("id"), status); err != nil {
		msg = err.Error()
	}
	if msg != "" {
		s.renderUI(w, sess.csrf, msg)
		return
	}
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	_, sess, ok := s.sessionFor(r)
	if !ok {
		s.renderUI(w, "", "")
		return
	}
	s.renderUI(w, sess.csrf, "")
}

// sameOrigin rejects cross-site form posts when the browser tells us the origin.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	return strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://") == r.Host
}

type uiData struct {
	LoggedIn bool
	CSRF     string
	Message  string
	Pending  []Record
	Recent   []Record
}

func (s *Server) renderUI(w http.ResponseWriter, csrf, msg string) {
	data := uiData{LoggedIn: csrf != "", CSRF: csrf, Message: msg}
	if data.LoggedIn {
		for _, rec := range s.snapshot() {
			if rec.Status == StatusPending {
				data.Pending = append(data.Pending, rec)
			} else {
				data.Recent = append(data.Recent, rec)
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplate.Execute(w, data); err != nil {
		log.Printf("ui: %v", err)
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

var uiTemplate = template.Must(template.New("ui").Funcs(template.FuncMap{
	"short": shortID,
	"ago": func(t time.Time) string {
		d := time.Since(t).Round(time.Second)
		switch {
		case d < time.Minute:
			return fmt.Sprintf("%ds ago", int(d.Seconds()))
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		default:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		}
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{if .LoggedIn}}<meta http-equiv="refresh" content="15">{{end}}
<title>Towline approvals</title>
<style>
:root { --bg:#f7f7f5; --fg:#1d1d1b; --muted:#6b6b66; --card:#fff; --line:#e3e3de; --ok:#1f7a3d; --no:#b42318; }
@media (prefers-color-scheme: dark) { :root { --bg:#161615; --fg:#ecece8; --muted:#9a9a93; --card:#20201e; --line:#33332f; --ok:#4cc27a; --no:#f07167; } }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font:15px/1.5 system-ui, sans-serif; }
main { max-width: 860px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 20px; margin: 0 0 4px; } h2 { font-size: 15px; margin: 28px 0 8px; color: var(--muted); text-transform: uppercase; letter-spacing: .04em; }
.card { background:var(--card); border:1px solid var(--line); border-radius:10px; padding:16px; margin:10px 0; }
.meta { color:var(--muted); font-size:13px; }
code, pre { font: 13px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; }
pre { background:var(--bg); border:1px solid var(--line); border-radius:6px; padding:10px; overflow:auto; max-height:360px; white-space:pre-wrap; word-break:break-word; }
.actions { display:flex; gap:8px; margin-top:12px; }
button { font:inherit; border-radius:8px; border:1px solid var(--line); background:var(--card); color:var(--fg); padding:8px 16px; cursor:pointer; }
button.approve { background:var(--ok); border-color:var(--ok); color:#fff; } button.reject { color:var(--no); border-color:var(--no); }
input[type=password] { font:inherit; width:100%; padding:8px 10px; border-radius:8px; border:1px solid var(--line); background:var(--bg); color:var(--fg); }
.msg { padding:10px 12px; border-radius:8px; border:1px solid var(--no); color:var(--no); margin:12px 0; }
.status-approved { color:var(--ok); } .status-rejected, .status-expired { color:var(--no); }
header { display:flex; justify-content:space-between; align-items:center; gap:12px; }
</style>
</head>
<body><main>
<header><div><h1>Towline approvals</h1><div class="meta">Production changes requested by your agents</div></div>
{{if .LoggedIn}}<form method="post" action="/ui/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Log out</button></form>{{end}}
</header>
{{with .Message}}<div class="msg">{{.}}</div>{{end}}
{{if not .LoggedIn}}
<div class="card">
<form method="post" action="/ui/login">
<p>Enter your approver token (shown once by <code>towline approvals setup</code>).</p>
<input type="password" name="token" autocomplete="current-password" autofocus>
<div class="actions"><button class="approve">Log in</button></div>
</form>
</div>
{{else}}
<h2>Pending ({{len .Pending}})</h2>
{{range .Pending}}
<div class="card">
<div><strong>{{.Action}}</strong> on <strong>{{.Project}}</strong></div>
<div class="meta">Requested {{ago .CreatedAt}} · id <code>{{short .ID}}</code></div>
{{with .Description}}<pre>{{.}}</pre>{{end}}
{{with .StackContent}}<details><summary>Compose file</summary><pre>{{.}}</pre></details>{{end}}
<form method="post" action="/ui/decide" class="actions">
<input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}">
<button class="approve" name="decision" value="approve">Approve</button>
<button class="reject" name="decision" value="reject">Reject</button>
</form>
</div>
{{else}}<div class="card meta">Nothing waiting for approval.</div>{{end}}
{{if .Recent}}<h2>Recent</h2>
{{range .Recent}}<div class="card"><strong>{{.Action}}</strong> on {{.Project}} · <span class="status-{{.Status}}">{{.Status}}</span> <span class="meta">· {{ago .CreatedAt}} · <code>{{short .ID}}</code></span></div>{{end}}
{{end}}
{{end}}
</main></body></html>`))
