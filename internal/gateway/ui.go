package gateway

import (
	"crypto/subtle"
	"net/http"

	"strings"
	"sync"
	"time"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/middleware"
)

const (
	ownerCookie     = "towline_gateway_owner"
	ownerSessionTTL = 12 * time.Hour
)

// ownerUI is the owner's web page: connections, pairings and approvals.
// The owner logs in with the owner token; forms carry a per-session CSRF
// token and must come from the gateway's own origin.
type ownerUI struct {
	g        *Gateway
	mu       sync.Mutex
	sessions map[string]ownerSession
}

type ownerSession struct {
	csrf    string
	expires time.Time
}

func newOwnerUI(g *Gateway) *ownerUI {
	return &ownerUI{g: g, sessions: map[string]ownerSession{}}
}

func (u *ownerUI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", u.handleDashboard)
	mux.HandleFunc("POST /ui/login", u.handleLogin)
	mux.HandleFunc("POST /ui/logout", u.withSession(u.handleLogout))
	mux.HandleFunc("POST /ui/connections", u.withSession(u.handleCreate))
	mux.HandleFunc("POST /ui/connections/revoke", u.withSession(u.handleRevoke))
	mux.HandleFunc("POST /ui/pairings/cancel", u.withSession(u.handleCancelPairing))
	mux.HandleFunc("POST /ui/approvals", u.withSession(u.handleDecide))
}

// --- Sessions ---

func (u *ownerUI) session(r *http.Request) (string, ownerSession, bool) {
	c, err := r.Cookie(ownerCookie)
	if err != nil || c.Value == "" {
		return "", ownerSession{}, false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	s, ok := u.sessions[hashSecret(c.Value)]
	if !ok || u.g.now().After(s.expires) {
		delete(u.sessions, hashSecret(c.Value))
		return "", ownerSession{}, false
	}
	return hashSecret(c.Value), s, true
}

func (u *ownerUI) loggedIn(r *http.Request) bool {
	_, _, ok := u.session(r)
	return ok
}

// loginAllowed reports whether another owner-token attempt is allowed from
// ip. The token is 256 bits, so this limits noise and lockout abuse rather
// than guessing.
func (u *ownerUI) loginAllowed(ip string) bool {
	return u.g.limiter.peek("loginfail:"+ip, limitLoginFailPerIP, 15*time.Minute) &&
		u.g.limiter.peek("loginfail:*", limitLoginFailGlobal, 15*time.Minute)
}

func (u *ownerUI) loginFailed(ip string) {
	u.g.limiter.allow("loginfail:"+ip, limitLoginFailPerIP, 15*time.Minute)
	u.g.limiter.allow("loginfail:*", limitLoginFailGlobal, 15*time.Minute)
	u.g.audit.Warn().Str("event", "owner_login_failed").Str("ip", ip).Msg("")
}

// withSession requires a logged-in owner, a same-origin post and the
// session's CSRF token.
func (u *ownerUI) withSession(next func(http.ResponseWriter, *http.Request, ownerSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, s, ok := u.session(r)
		if !ok {
			http.Redirect(w, r, "/ui", http.StatusSeeOther)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if !u.g.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
			http.Error(w, "invalid form submission", http.StatusForbidden)
			return
		}
		next(w, r, s)
	}
}

func (u *ownerUI) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !u.g.sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	ip := u.g.clientIP(r)
	if !u.loginAllowed(ip) {
		tooMany(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !u.g.isOwnerToken(strings.TrimSpace(r.PostForm.Get("token"))) {
		u.loginFailed(ip)
		u.render(w, http.StatusUnauthorized, "login", map[string]any{"Message": "Wrong owner token."})
		return
	}
	id := randomHex(32)
	u.mu.Lock()
	now := u.g.now()
	for k, s := range u.sessions {
		if now.After(s.expires) {
			delete(u.sessions, k)
		}
	}
	u.sessions[hashSecret(id)] = ownerSession{csrf: randomHex(32), expires: now.Add(ownerSessionTTL)}
	u.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     ownerCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   u.g.publicURL.Scheme == "https",
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ownerSessionTTL.Seconds()),
	})
	u.g.audit.Info().Str("event", "owner_login").Str("ip", ip).Msg("")
	next := r.PostForm.Get("next")
	if !strings.HasPrefix(next, "/oauth/authorize?") {
		next = "/ui"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (u *ownerUI) handleLogout(w http.ResponseWriter, r *http.Request, _ ownerSession) {
	if id, _, ok := u.session(r); ok {
		u.mu.Lock()
		delete(u.sessions, id)
		u.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: u.g.publicURL.Scheme == "https", SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

// --- Dashboard ---

type projectView struct {
	Name, Stack, Tier, Approval string
}

type dashboardData struct {
	CSRF        string
	Message     string
	PublicURL   string
	MCPURL      string
	Projects    []projectView
	Connections []Connection
	Pairings    []Pairing
	Pending     []approval.Record
	Recent      []approval.Record
}

func (u *ownerUI) dashboard(s ownerSession, msg string) dashboardData {
	d := dashboardData{CSRF: s.csrf, Message: msg, PublicURL: u.g.cfg.PublicURL, MCPURL: u.g.resourceFor("")}
	for _, p := range u.g.cfg.Projects {
		d.Projects = append(d.Projects, projectView{Name: p.Name, Stack: p.Stack, Tier: p.Tier, Approval: p.Approval})
	}
	d.Connections = u.g.store.Connections()
	d.Pairings = u.g.store.Pairings()
	for _, rec := range u.g.approvals.Records() {
		if rec.Status == approval.StatusPending {
			d.Pending = append(d.Pending, rec)
		} else if len(d.Recent) < 20 {
			d.Recent = append(d.Recent, rec)
		}
	}
	return d
}

func (u *ownerUI) handleDashboard(w http.ResponseWriter, r *http.Request) {
	_, s, ok := u.session(r)
	if !ok {
		u.render(w, http.StatusOK, "login", map[string]any{})
		return
	}
	u.render(w, http.StatusOK, "dashboard", u.dashboard(s, ""))
}

func (u *ownerUI) handleCreate(w http.ResponseWriter, r *http.Request, s ownerSession) {
	f := r.PostForm
	known := func(name string) bool { _, ok := u.g.cfg.Project(name); return ok }
	scope := middleware.Scope(f.Get("scope"))
	projects := f["project"]
	method := f.Get("method")

	if method == "app" {
		p, err := u.g.store.OpenPairing(f.Get("name"), projects, scope, known)
		if err != nil {
			u.render(w, http.StatusBadRequest, "dashboard", u.dashboard(s, err.Error()))
			return
		}
		u.g.audit.Info().Str("event", "pairing_opened").Str("pairing", p.ID).Strs("projects", p.Projects).Str("scope", string(p.Scope)).Msg("")
		u.render(w, http.StatusOK, "paired", map[string]any{"Pairing": p, "URLs": u.endpointURLs(p.Projects), "CSRF": s.csrf})
		return
	}
	kind := KindToken
	if method == "oauth" {
		kind = KindOAuth
	} else if method != "token" {
		u.render(w, http.StatusBadRequest, "dashboard", u.dashboard(s, "Choose how the app connects."))
		return
	}
	var redirects []string
	if kind == KindOAuth {
		redirects = strings.Fields(f.Get("redirect_uris"))
	}
	c, err := u.g.store.CreateConnection(NewConnectionRequest{Name: f.Get("name"), Kind: kind, Projects: projects, Scope: scope, RedirectURIs: redirects}, known)
	if err != nil {
		u.render(w, http.StatusBadRequest, "dashboard", u.dashboard(s, err.Error()))
		return
	}
	u.g.audit.Info().Str("event", "connection_created").Str("connection", c.ID).Str("kind", c.Kind).Strs("projects", c.Projects).Str("scope", string(c.Scope)).Msg("")
	u.render(w, http.StatusOK, "created", map[string]any{"Conn": c, "URLs": u.endpointURLs(c.Projects), "CSRF": s.csrf})
}

type endpointURL struct {
	Label, URL string
}

// endpointURLs lists the MCP URLs a connection can use: /mcp for all its
// projects, and a per-project URL for each (fewer tools, plain names).
func (u *ownerUI) endpointURLs(projects []string) []endpointURL {
	out := []endpointURL{{Label: "All projects of this connection", URL: u.g.resourceFor("")}}
	for _, p := range projects {
		out = append(out, endpointURL{Label: "Only " + p, URL: u.g.resourceFor(p)})
	}
	return out
}

func (u *ownerUI) handleRevoke(w http.ResponseWriter, r *http.Request, s ownerSession) {
	id := r.PostForm.Get("id")
	if err := u.g.store.RevokeConnection(id); err != nil {
		u.render(w, http.StatusBadRequest, "dashboard", u.dashboard(s, err.Error()))
		return
	}
	u.g.audit.Info().Str("event", "connection_revoked").Str("connection", id).Msg("")
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func (u *ownerUI) handleCancelPairing(w http.ResponseWriter, r *http.Request, s ownerSession) {
	u.g.store.CancelPairing(r.PostForm.Get("id"))
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func (u *ownerUI) handleDecide(w http.ResponseWriter, r *http.Request, s ownerSession) {
	status := approval.StatusRejected
	if r.PostForm.Get("decision") == "approve" {
		status = approval.StatusApproved
	}
	id := r.PostForm.Get("id")
	if err := u.g.approvals.Decide(id, status); err != nil {
		u.render(w, http.StatusConflict, "dashboard", u.dashboard(s, err.Error()))
		return
	}
	u.g.audit.Info().Str("event", "approval_decided").Str("decision", string(status)).Str("request", shortHash(id)).Msg("")
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func shortHash(id string) string {
	return hashSecret(id)[:12]
}

func (u *ownerUI) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pageTemplates.ExecuteTemplate(w, name, data)
}
