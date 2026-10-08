package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// This file implements a deliberately small OAuth 2.1 authorization server
// for MCP clients (MCP authorization spec 2025-06-18):
//
//   - RFC 9728 protected resource metadata and RFC 8414 server metadata
//   - authorization code flow with PKCE (S256 only) and RFC 8707 resource
//     indicators; every token is bound to one MCP endpoint
//   - RFC 7591 dynamic registration, open only while the owner has a
//     pairing open, and every registered client needs the owner's approval
//   - pre-registered confidential clients (client id + secret created in
//     the owner UI), which skip the consent page because the secret is
//     needed to redeem the code
//
// What a token may do is never stored in the token: projects and scope are
// read from the connection on every request, so editing or revoking a
// connection applies immediately.

const authzRequestTTL = 10 * time.Minute

// scopesSupported are advertised for clients that want a scope list; the
// requested scope is ignored (the connection decides).
var scopesSupported = []string{"read", "deploy"}

// --- Discovery ---

// resourceFor returns the canonical resource URL for an MCP endpoint.
func (g *Gateway) resourceFor(project string) string {
	if project == "" {
		return g.cfg.PublicURL + "/mcp"
	}
	return g.cfg.PublicURL + "/p/" + project + "/mcp"
}

// resourceMetadataURL is where clients find the metadata for an endpoint.
func (g *Gateway) resourceMetadataURL(project string) string {
	if project == "" {
		return g.cfg.PublicURL + "/.well-known/oauth-protected-resource/mcp"
	}
	return g.cfg.PublicURL + "/.well-known/oauth-protected-resource/p/" + project + "/mcp"
}

func (g *Gateway) handleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if project != "" && !ValidProjectName(project) {
		http.NotFound(w, r)
		return
	}
	// Any well-formed project name gets metadata, so this endpoint doesn't
	// reveal which projects exist.
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 g.resourceFor(project),
		"authorization_servers":    []string{g.cfg.PublicURL},
		"scopes_supported":         scopesSupported,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Towline",
	})
}

func (g *Gateway) handleServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := g.cfg.PublicURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         base,
		"authorization_endpoint":                         base + "/oauth/authorize",
		"token_endpoint":                                 base + "/oauth/token",
		"registration_endpoint":                          base + "/oauth/register",
		"revocation_endpoint":                            base + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"client_secret_basic", "client_secret_post", "none"},
		"revocation_endpoint_auth_methods_supported":     []string{"client_secret_basic", "client_secret_post", "none"},
		"scopes_supported":                               scopesSupported,
		"authorization_response_iss_parameter_supported": true,
	})
}

// --- Redirect URIs ---

// customSchemePattern accepts private-use URI schemes of native apps in
// reverse-domain form (RFC 8252 7.1), e.g. cursor.app://..., and never
// javascript:, data: and the like.
var customSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+-]*(\.[a-z0-9+-]+)+$`)

// validateRedirectURI accepts https URLs, loopback http URLs and
// reverse-domain custom schemes, without fragments or credentials.
func validateRedirectURI(raw string) error {
	if len(raw) > 512 {
		return errors.New("redirect URI is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || strings.ContainsAny(raw, " \t\r\n<>\"'`\\") {
		return fmt.Errorf("invalid redirect URI %q", raw)
	}
	switch {
	case u.Scheme == "https" && u.Host != "":
		return nil
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
		return nil
	case customSchemePattern.MatchString(u.Scheme):
		return nil
	}
	return fmt.Errorf("redirect URI %q must be https, loopback http, or an app scheme", raw)
}

// redirectMatches compares a requested redirect URI with a registered one:
// exactly, except that loopback URIs may use any port (RFC 8252 7.3).
func redirectMatches(registered, requested string) bool {
	if registered == requested {
		return true
	}
	ru, err1 := url.Parse(registered)
	qu, err2 := url.Parse(requested)
	if err1 != nil || err2 != nil || ru.Scheme != "http" || qu.Scheme != "http" {
		return false
	}
	if !isLoopbackHost(ru.Hostname()) || ru.Hostname() != qu.Hostname() {
		return false
	}
	return ru.Path == qu.Path && ru.RawQuery == qu.RawQuery && qu.Fragment == "" && qu.User == nil
}

func pickRedirect(registered []string, requested string) (string, bool) {
	if requested == "" {
		if len(registered) == 1 {
			return registered[0], true
		}
		return "", false
	}
	for _, r := range registered {
		if redirectMatches(r, requested) {
			return requested, true
		}
	}
	return "", false
}

// --- Dynamic client registration ---

func (g *Gateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	ip := g.clientIP(r)
	if !g.limiter.allow("oauth:"+ip, limitOAuthPerIP, time.Minute) || !g.limiter.allow("register:"+ip, limitRegisterPerIP, 10*time.Minute) {
		tooMany(w)
		return
	}
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFormBytes)).Decode(&req); err != nil {
		registerError(w, "invalid_client_metadata", "invalid JSON body")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 5 {
		registerError(w, "invalid_redirect_uri", "between 1 and 5 redirect_uris are required")
		return
	}
	for _, u := range req.RedirectURIs {
		if err := validateRedirectURI(u); err != nil {
			registerError(w, "invalid_redirect_uri", err.Error())
			return
		}
	}
	for _, gt := range req.GrantTypes {
		if gt != "authorization_code" && gt != "refresh_token" {
			registerError(w, "invalid_client_metadata", "unsupported grant type "+gt)
			return
		}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			registerError(w, "invalid_client_metadata", "unsupported response type "+rt)
			return
		}
	}
	method := req.TokenEndpointAuthMethod
	switch method {
	case "":
		method = "client_secret_basic"
	case "none", "client_secret_basic", "client_secret_post":
	default:
		registerError(w, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return
	}
	name := cleanClientName(req.ClientName)

	clientID, secret, err := g.store.registerClient(name, req.RedirectURIs, method != "none")
	if err != nil {
		registerError(w, "invalid_client_metadata", err.Error())
		return
	}
	g.audit.Info().Str("event", "client_registered").Str("client_id", clientID).Str("client_name", name).
		Strs("redirect_uris", req.RedirectURIs).Str("ip", g.clientIP(r)).Msg("")
	resp := map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        g.now().Unix(),
		"client_name":                name,
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": method,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, resp)
}

func registerError(w http.ResponseWriter, code, desc string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": desc})
}

// cleanClientName keeps a client-chosen name short and printable. It is
// shown on the consent page as a claim, never trusted.
func cleanClientName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return "Unnamed app"
	}
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	return name
}

// --- Authorization ---

// authzRequest is a validated authorization request waiting for the
// owner's decision on the consent page.
type authzRequest struct {
	ID            string
	ClientID      string
	ClientName    string
	RedirectURI   string
	RedirectGiven bool
	State         string
	Resource      string
	CodeChallenge string
	// Pending is set for a dynamically registered client not yet approved.
	Pending   bool
	ExpiresAt time.Time
}

// handleAuthorize validates the request and either issues a code (a
// confidential client the owner created) or shows the consent page.
//
// Errors about the client or redirect URI are shown here and never
// redirected, so this endpoint can't be used as an open redirect.
func (g *Gateway) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if !g.limiter.allow("oauth:"+g.clientIP(r), limitOAuthPerIP, time.Minute) {
		tooMany(w)
		return
	}
	q := r.URL.Query()
	for _, k := range []string{"client_id", "redirect_uri", "response_type", "code_challenge", "code_challenge_method", "state", "resource"} {
		if len(q[k]) > 1 {
			g.authzErrorPage(w, "The request repeats the "+k+" parameter.")
			return
		}
	}
	clientID := q.Get("client_id")
	conn := g.store.oauthClient(clientID)
	var pending *pendingClient
	var registered []string
	var clientName string
	switch {
	case conn != nil:
		registered, clientName = conn.RedirectURIs, conn.Name
	default:
		pending = g.store.pendingClientByID(clientID)
		if pending == nil {
			g.authzErrorPage(w, "This app is not registered with the gateway (or its registration expired). Open a pairing on the gateway page and add the connector again.")
			return
		}
		registered, clientName = pending.RedirectURIs, pending.ClientName
	}
	redirectGiven := q.Get("redirect_uri") != ""
	redirectURI, ok := pickRedirect(registered, q.Get("redirect_uri"))
	if !ok {
		g.authzErrorPage(w, "The redirect URI does not match the one registered for this app.")
		return
	}

	// From here on, errors go back to the client.
	state := q.Get("state")
	if q.Get("response_type") != "code" {
		g.redirectError(w, r, redirectURI, state, "unsupported_response_type", "only the authorization code flow is supported")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !validChallenge(challenge) {
		g.redirectError(w, r, redirectURI, state, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}
	resource, err := g.checkResource(q.Get("resource"), conn)
	if err != nil {
		g.redirectError(w, r, redirectURI, state, "invalid_target", err.Error())
		return
	}

	// A confidential client the owner created (or approved) can only
	// redeem a code with its secret, so it needs no consent screen.
	if conn != nil && !conn.Public() {
		g.issueCodeRedirect(w, r, conn, redirectURI, redirectGiven, resource, challenge, state)
		return
	}

	req := &authzRequest{
		ID:            randomHex(32),
		ClientID:      clientID,
		ClientName:    clientName,
		RedirectURI:   redirectURI,
		RedirectGiven: redirectGiven,
		State:         state,
		Resource:      resource,
		CodeChallenge: challenge,
		Pending:       pending != nil,
		ExpiresAt:     g.now().Add(authzRequestTTL),
	}
	if !g.saveAuthzRequest(req) {
		g.authzErrorPage(w, "Too many sign-ins are waiting. Try again in a few minutes.")
		return
	}
	g.renderConsent(w, r, req, conn, "")
}

// checkResource validates an RFC 8707 resource indicator: the gateway's
// /mcp endpoint, or a project endpoint the connection (if known) may use.
// No resource means /mcp.
func (g *Gateway) checkResource(resource string, conn *Connection) (string, error) {
	if resource == "" {
		return g.resourceFor(""), nil
	}
	resource = strings.TrimSuffix(resource, "/")
	if resource == g.resourceFor("") {
		return resource, nil
	}
	if rest, ok := strings.CutPrefix(resource, g.cfg.PublicURL+"/p/"); ok {
		if name, ok := strings.CutSuffix(rest, "/mcp"); ok && ValidProjectName(name) {
			if _, exists := g.cfg.Project(name); exists && (conn == nil || conn.HasProject(name)) {
				return resource, nil
			}
		}
	}
	return "", errors.New("unknown resource for this client")
}

func validChallenge(c string) bool {
	if len(c) < 43 || len(c) > 128 {
		return false
	}
	for _, r := range c {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' || r == '~') {
			return false
		}
	}
	return true
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || !validChallenge(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func (g *Gateway) saveAuthzRequest(req *authzRequest) bool {
	g.authzMu.Lock()
	defer g.authzMu.Unlock()
	now := g.now()
	for id, a := range g.authzReq {
		if now.After(a.ExpiresAt) {
			delete(g.authzReq, id)
		}
	}
	if len(g.authzReq) >= 100 {
		return false
	}
	g.authzReq[req.ID] = req
	return true
}

// takeAuthzRequest removes and returns a pending authorization request.
func (g *Gateway) takeAuthzRequest(id string) *authzRequest {
	g.authzMu.Lock()
	defer g.authzMu.Unlock()
	a := g.authzReq[id]
	if a == nil {
		return nil
	}
	delete(g.authzReq, id)
	if g.now().After(a.ExpiresAt) {
		return nil
	}
	return a
}

func (g *Gateway) peekAuthzRequest(id string) *authzRequest {
	g.authzMu.Lock()
	defer g.authzMu.Unlock()
	a := g.authzReq[id]
	if a == nil || g.now().After(a.ExpiresAt) {
		return nil
	}
	cp := *a
	return &cp
}

// handleConsent processes the owner's decision on the consent page.
func (g *Gateway) handleConsent(w http.ResponseWriter, r *http.Request) {
	if !g.sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.PostForm.Get("request")
	req := g.peekAuthzRequest(id)
	if req == nil {
		g.authzErrorPage(w, "This sign-in request expired. Start again from the app.")
		return
	}
	conn := g.store.oauthClient(req.ClientID)

	// The owner proves who they are with a UI session or the owner token.
	if !g.ui.loggedIn(r) {
		ip := g.clientIP(r)
		if !g.ui.loginAllowed(ip) {
			tooMany(w)
			return
		}
		if !g.isOwnerToken(strings.TrimSpace(r.PostForm.Get("owner_token"))) {
			g.ui.loginFailed(ip)
			g.renderConsent(w, r, req, conn, "Wrong owner token.")
			return
		}
	}

	if r.PostForm.Get("decision") != "approve" {
		g.takeAuthzRequest(id)
		g.audit.Info().Str("event", "consent_denied").Str("client_id", req.ClientID).Msg("")
		g.redirectError(w, r, req.RedirectURI, req.State, "access_denied", "the owner denied access")
		return
	}

	if req.Pending {
		var err error
		conn, err = g.store.approveClient(req.ClientID, r.PostForm.Get("pairing"))
		if err != nil {
			g.renderConsent(w, r, req, nil, err.Error())
			return
		}
		g.audit.Info().Str("event", "client_approved").Str("connection", conn.ID).Str("client_id", conn.ClientID).
			Str("client_name", conn.ClientName).Strs("projects", conn.Projects).Str("scope", string(conn.Scope)).Msg("")
	}
	if conn == nil {
		g.authzErrorPage(w, "This app's connection was revoked.")
		return
	}
	// The resource was checked before the projects were known.
	if _, err := g.checkResource(req.Resource, conn); err != nil {
		g.takeAuthzRequest(id)
		g.redirectError(w, r, req.RedirectURI, req.State, "invalid_target", err.Error())
		return
	}
	if g.takeAuthzRequest(id) == nil {
		g.authzErrorPage(w, "This sign-in request expired. Start again from the app.")
		return
	}
	g.issueCodeRedirect(w, r, conn, req.RedirectURI, req.RedirectGiven, req.Resource, req.CodeChallenge, req.State)
}

func (g *Gateway) issueCodeRedirect(w http.ResponseWriter, r *http.Request, conn *Connection, redirectURI string, redirectGiven bool, resource, challenge, state string) {
	code, err := g.store.issueCode(conn, redirectURI, redirectGiven, resource, challenge)
	if err != nil {
		g.redirectError(w, r, redirectURI, state, "temporarily_unavailable", err.Error())
		return
	}
	g.audit.Info().Str("event", "code_issued").Str("connection", conn.ID).Str("resource", resource).Msg("")
	params := url.Values{"code": {code}, "iss": {g.cfg.PublicURL}}
	if state != "" {
		params.Set("state", state)
	}
	g.redirect(w, r, redirectURI, params)
}

func (g *Gateway) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	params := url.Values{"error": {code}, "error_description": {desc}, "iss": {g.cfg.PublicURL}}
	if state != "" {
		params.Set("state", state)
	}
	g.redirect(w, r, redirectURI, params)
}

// redirect sends the browser to a validated redirect URI with params added.
func (g *Gateway) redirect(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		g.authzErrorPage(w, "invalid redirect URI")
		return
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	// Browsers apply form-action to redirects after a form post, so allow
	// this one destination.
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; form-action 'self' %s; frame-ancestors 'none'", cspSource(u)))
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func cspSource(u *url.URL) string {
	if u.Host == "" {
		return u.Scheme + ":"
	}
	return u.Scheme + "://" + u.Host
}

// --- Token endpoint ---

func (g *Gateway) handleToken(w http.ResponseWriter, r *http.Request) {
	ip := g.clientIP(r)
	if !g.limiter.allow("oauth:"+ip, limitOAuthPerIP, time.Minute) {
		tooMany(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	f := r.PostForm
	conn, ok := g.clientFromRequest(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="towline"`)
		tokenError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}

	var pair *tokenPair
	var resource string
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		pair, resource, err = g.store.redeemCode(conn, f.Get("code"), f.Get("redirect_uri"), f.Get("code_verifier"), strings.TrimSuffix(f.Get("resource"), "/"))
	case "refresh_token":
		pair, resource, err = g.store.refreshAccess(conn, f.Get("refresh_token"), strings.TrimSuffix(f.Get("resource"), "/"))
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
		return
	}
	if err != nil {
		g.audit.Info().Str("event", "token_refused").Str("connection", conn.ID).Str("grant_type", f.Get("grant_type")).Str("reason", err.Error()).Msg("")
		tokenError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	g.audit.Info().Str("event", "token_issued").Str("connection", conn.ID).Str("grant_type", f.Get("grant_type")).Str("resource", resource).Msg("")
	resp := map[string]any{
		"access_token": pair.Access,
		"token_type":   "Bearer",
		"expires_in":   int(accessTokenTTL.Seconds()),
		"scope":        string(conn.Scope),
	}
	if pair.Refresh != "" {
		resp["refresh_token"] = pair.Refresh
	}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, resp)
}

// clientFromRequest authenticates the OAuth client (HTTP Basic or form
// credentials; never both).
func (g *Gateway) clientFromRequest(r *http.Request) (*Connection, bool) {
	f := r.PostForm
	id, secret, basic := r.BasicAuth()
	if basic {
		if f.Get("client_secret") != "" {
			return nil, false
		}
		var err1, err2 error
		id, err1 = url.QueryUnescape(id)
		secret, err2 = url.QueryUnescape(secret)
		if err1 != nil || err2 != nil {
			return nil, false
		}
		if fid := f.Get("client_id"); fid != "" && fid != id {
			return nil, false
		}
	} else {
		id, secret = f.Get("client_id"), f.Get("client_secret")
	}
	c := g.store.authenticateClient(id, secret)
	return c, c != nil
}

func tokenError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (g *Gateway) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if !g.limiter.allow("oauth:"+g.clientIP(r), limitOAuthPerIP, time.Minute) {
		tooMany(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	conn, ok := g.clientFromRequest(r)
	if !ok {
		tokenError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	g.store.revokeToken(conn, r.PostForm.Get("token"))
	w.WriteHeader(http.StatusOK)
}

// --- Pages ---

func (g *Gateway) authzErrorPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = pageTemplates.ExecuteTemplate(w, "error", map[string]any{"Message": msg})
}

type consentData struct {
	Request    *authzRequest
	ClientName string
	Redirect   string
	Projects   []string
	Scope      string
	Pairings   []Pairing
	LoggedIn   bool
	Message    string
	Public     bool
}

func (g *Gateway) renderConsent(w http.ResponseWriter, r *http.Request, req *authzRequest, conn *Connection, msg string) {
	d := consentData{Request: req, ClientName: req.ClientName, LoggedIn: g.ui.loggedIn(r), Message: msg}
	if u, err := url.Parse(req.RedirectURI); err == nil {
		d.Redirect = u.Scheme + "://" + u.Host
		if u.Host == "" {
			d.Redirect = u.Scheme + ":"
		}
	}
	if conn != nil {
		d.Projects, d.Scope = conn.Projects, string(conn.Scope)
	} else {
		d.Pairings = g.store.Pairings()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if msg != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = pageTemplates.ExecuteTemplate(w, "consent", d)
}
