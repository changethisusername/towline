package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/changethisusername/towline/internal/middleware"
)

// Token prefixes make each credential's kind visible (and findable by
// secret scanners). A credential is only ever accepted where its kind is
// expected: a client secret is never a bearer token, and so on.
const (
	prefixClientToken  = "twl_ct_" // static bearer token of a "token" connection
	prefixClientSecret = "twl_cs_" // OAuth client secret, accepted only at /oauth/token
	prefixAccessToken  = "twl_at_" // OAuth access token
	prefixRefreshToken = "twl_rt_" // OAuth refresh token
	prefixAuthCode     = "twl_ac_" // OAuth authorization code
	prefixClientID     = "twl_ci_" // OAuth client id (not secret)
)

const (
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
	authCodeTTL     = 60 * time.Second
	// refreshGrace is how long a rotated refresh token keeps working.
	refreshGrace = 2 * time.Minute
	// maxCodesPerClient bounds outstanding authorization codes, since
	// anyone who knows a client id can request them.
	maxCodesPerClient = 20
	maxConnections    = 200
	// maxTokensPerConnection bounds stored OAuth tokens per connection.
	maxTokensPerConnection = 50
)

// Connection kinds.
const (
	// KindToken connections authenticate with a static bearer token.
	KindToken = "token"
	// KindOAuth connections are pre-registered confidential OAuth clients
	// (e.g. a Claude.ai custom connector's client id and secret).
	KindOAuth = "oauth"
	// KindApp connections are OAuth clients that registered themselves
	// (dynamic client registration) while the owner had a pairing open,
	// and that the owner then approved on the consent page.
	KindApp = "app"
)

// pairingTTL is how long a pairing stays open for an app to register and
// sign in.
const pairingTTL = 10 * time.Minute

// maxPendingClients bounds dynamically registered clients waiting for
// consent.
const maxPendingClients = 20

// DefaultRedirectURIs are the OAuth callbacks of Claude and ChatGPT
// connectors.
var DefaultRedirectURIs = []string{
	"https://claude.ai/api/mcp/auth_callback",
	"https://claude.com/api/mcp/auth_callback",
	"https://chatgpt.com/connector_platform_oauth_redirect",
}

// Connection is an owner-issued credential for one or more projects.
type Connection struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Kind     string           `json:"kind"`
	Projects []string         `json:"projects"`
	Scope    middleware.Scope `json:"scope"`
	// SecretHash is the SHA-256 of the bearer token (token kind) or the
	// client secret (oauth kind, and app clients that asked for one).
	// Public app clients have none and rely on PKCE.
	SecretHash   string    `json:"secret_hash,omitempty"`
	ClientName   string    `json:"client_name,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	RedirectURIs []string  `json:"redirect_uris,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at,omitzero"`
}

// HasProject reports whether the connection may use a project.
func (c *Connection) HasProject(name string) bool {
	return slices.Contains(c.Projects, name)
}

// Public reports whether the connection is an OAuth client without a
// secret.
func (c *Connection) Public() bool {
	return c.Kind == KindApp && c.SecretHash == ""
}

// Pairing is an owner-opened window in which one app may register and be
// approved, getting the pairing's projects and scope.
type Pairing struct {
	ID        string
	Name      string
	Projects  []string
	Scope     middleware.Scope
	ExpiresAt time.Time
}

// pendingClient is a dynamically registered client awaiting consent.
type pendingClient struct {
	ClientID     string
	SecretHash   string
	ClientName   string
	RedirectURIs []string
	ExpiresAt    time.Time
}

// oauthToken is a stored access or refresh token.
type oauthToken struct {
	ConnectionID string    `json:"connection_id"`
	Resource     string    `json:"resource"`
	ExpiresAt    time.Time `json:"expires_at"`
	// CodeHash links tokens to the authorization code that minted them, so
	// a replayed code revokes them.
	CodeHash string `json:"code_hash,omitempty"`
	// Successors are the refresh tokens that replaced this one (public
	// clients), and Used records that this one was redeemed. Redeeming a
	// replaced token after its successor was used means the token was
	// copied, and revokes the whole grant.
	Successors []string `json:"successors,omitempty"`
	Used       bool     `json:"used,omitempty"`
}

// authCode is a pending authorization code (memory only).
type authCode struct {
	ConnectionID string
	ClientID     string
	RedirectURI  string
	// RedirectGiven records whether the authorization request named the
	// redirect URI; only then must the token request repeat it.
	RedirectGiven bool
	Resource      string
	CodeChallenge string
	ExpiresAt     time.Time
	Used          bool
}

// stateFile is what is persisted.
type stateFile struct {
	Version       int                    `json:"version"`
	Connections   []*Connection          `json:"connections"`
	AccessTokens  map[string]*oauthToken `json:"access_tokens"`
	RefreshTokens map[string]*oauthToken `json:"refresh_tokens"`
}

// Store holds connections and OAuth state, persisted to a JSON file
// (0600) so that revocations and refresh tokens survive restarts. Only
// hashes of secrets are stored.
type Store struct {
	mu   sync.Mutex
	path string
	now  func() time.Time

	conns   map[string]*Connection // by id
	secrets map[string]string      // secret hash -> connection id
	clients map[string]string      // client id -> connection id
	access  map[string]*oauthToken // token hash -> token
	refresh map[string]*oauthToken
	codes   map[string]*authCode // code hash -> code

	pairings map[string]*Pairing       // by id
	pending  map[string]*pendingClient // by client id
}

// OpenStore loads the store from path, creating it if missing.
func OpenStore(path string) (*Store, error) {
	s := &Store{
		path:    path,
		now:     time.Now,
		conns:   map[string]*Connection{},
		secrets: map[string]string{},
		clients: map[string]string{},
		access:  map[string]*oauthToken{},
		refresh: map[string]*oauthToken{},
		codes:   map[string]*authCode{},

		pairings: map[string]*Pairing{},
		pending:  map[string]*pendingClient{},
	}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, s.saveLocked()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state: %w", err)
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse state %s: %w", path, err)
	}
	for _, c := range f.Connections {
		if c == nil || c.ID == "" || (c.SecretHash == "" && c.Kind != KindApp) {
			continue
		}
		s.conns[c.ID] = c
		if c.SecretHash != "" {
			s.secrets[c.SecretHash] = c.ID
		}
		if c.ClientID != "" {
			s.clients[c.ClientID] = c.ID
		}
	}
	for h, t := range f.AccessTokens {
		if t != nil && s.conns[t.ConnectionID] != nil {
			s.access[h] = t
		}
	}
	for h, t := range f.RefreshTokens {
		if t != nil && s.conns[t.ConnectionID] != nil {
			s.refresh[h] = t
		}
	}
	return s, nil
}

// saveLocked writes the state atomically.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	s.pruneLocked()
	f := stateFile{Version: 1, AccessTokens: s.access, RefreshTokens: s.refresh}
	for _, c := range s.conns {
		f.Connections = append(f.Connections, c)
	}
	slices.SortFunc(f.Connections, func(a, b *Connection) int { return a.CreatedAt.Compare(b.CreatedAt) })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (s *Store) pruneLocked() {
	now := s.now()
	for h, t := range s.access {
		if now.After(t.ExpiresAt) {
			delete(s.access, h)
		}
	}
	for h, t := range s.refresh {
		if now.After(t.ExpiresAt) {
			delete(s.refresh, h)
		}
	}
	for h, c := range s.codes {
		if now.After(c.ExpiresAt) {
			delete(s.codes, h)
		}
	}
	for id, p := range s.pairings {
		if now.After(p.ExpiresAt) {
			delete(s.pairings, id)
		}
	}
	for id, p := range s.pending {
		if now.After(p.ExpiresAt) {
			delete(s.pending, id)
		}
	}
}

// NewConnectionRequest describes a connection to create.
type NewConnectionRequest struct {
	Name         string
	Kind         string
	Projects     []string
	Scope        middleware.Scope
	RedirectURIs []string
}

// CreatedConnection is returned once on creation, with the plaintext secret.
type CreatedConnection struct {
	Connection
	// Secret is the bearer token (token kind) or client secret (oauth kind).
	// It is shown once and never stored.
	Secret string
}

// validateGrant checks a connection's name, projects and scope.
func validateGrant(rawName string, projects []string, scope middleware.Scope, knownProject func(string) bool) (string, []string, error) {
	name := strings.TrimSpace(rawName)
	if name == "" || len(name) > 64 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", nil, errors.New("name must be 1-64 printable characters")
	}
	if _, err := middleware.ParseScope(string(scope)); err != nil {
		return "", nil, err
	}
	if len(projects) == 0 {
		return "", nil, errors.New("pick at least one project")
	}
	var out []string
	for _, p := range projects {
		if !knownProject(p) {
			return "", nil, fmt.Errorf("unknown project %q", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return name, out, nil
}

// CreateConnection validates and stores a new token or oauth connection.
// knownProject reports whether a project exists.
func (s *Store) CreateConnection(req NewConnectionRequest, knownProject func(string) bool) (*CreatedConnection, error) {
	if req.Kind != KindToken && req.Kind != KindOAuth {
		return nil, errors.New("kind must be token or oauth")
	}
	name, projects, err := validateGrant(req.Name, req.Projects, req.Scope, knownProject)
	if err != nil {
		return nil, err
	}
	var redirects []string
	if req.Kind == KindOAuth {
		redirects = append(redirects, DefaultRedirectURIs...)
		for _, u := range req.RedirectURIs {
			if u = strings.TrimSpace(u); u == "" {
				continue
			}
			if err := validateRedirectURI(u); err != nil {
				return nil, err
			}
			if !slices.Contains(redirects, u) {
				redirects = append(redirects, u)
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) >= maxConnections {
		return nil, errors.New("too many connections; revoke some first")
	}
	c := &Connection{
		ID:           "conn_" + randomHex(8),
		Name:         name,
		Kind:         req.Kind,
		Projects:     projects,
		Scope:        req.Scope,
		RedirectURIs: redirects,
		CreatedAt:    s.now().UTC(),
	}
	var secret string
	if req.Kind == KindToken {
		secret = prefixClientToken + randomHex(32)
	} else {
		secret = prefixClientSecret + randomHex(32)
		c.ClientID = prefixClientID + randomHex(16)
	}
	c.SecretHash = hashSecret(secret)
	s.conns[c.ID] = c
	s.secrets[c.SecretHash] = c.ID
	if c.ClientID != "" {
		s.clients[c.ClientID] = c.ID
	}
	if err := s.saveLocked(); err != nil {
		s.deleteLocked(c.ID)
		return nil, err
	}
	return &CreatedConnection{Connection: *c, Secret: secret}, nil
}

// RevokeConnection deletes a connection and every token it was issued.
func (s *Store) RevokeConnection(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conns[id]; !ok {
		return errors.New("unknown connection")
	}
	s.deleteLocked(id)
	return s.saveLocked()
}

func (s *Store) deleteLocked(id string) {
	c, ok := s.conns[id]
	if !ok {
		return
	}
	delete(s.conns, id)
	if c.SecretHash != "" {
		delete(s.secrets, c.SecretHash)
	}
	if c.ClientID != "" {
		delete(s.clients, c.ClientID)
	}
	for h, t := range s.access {
		if t.ConnectionID == id {
			delete(s.access, h)
		}
	}
	for h, t := range s.refresh {
		if t.ConnectionID == id {
			delete(s.refresh, h)
		}
	}
	for h, c := range s.codes {
		if c.ConnectionID == id {
			delete(s.codes, h)
		}
	}
}

// DropProject removes a project from every connection (when the project is
// removed from the gateway). Connections left with no project are revoked.
func (s *Store) DropProject(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, c := range s.conns {
		if !c.HasProject(name) {
			continue
		}
		changed = true
		c.Projects = slices.DeleteFunc(c.Projects, func(p string) bool { return p == name })
		if len(c.Projects) == 0 {
			s.deleteLocked(id)
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// DropProjectsExcept removes every project not in keep from all
// connections.
func (s *Store) DropProjectsExcept(keep []string) error {
	s.mu.Lock()
	var gone []string
	for _, c := range s.conns {
		for _, p := range c.Projects {
			if !slices.Contains(keep, p) && !slices.Contains(gone, p) {
				gone = append(gone, p)
			}
		}
	}
	s.mu.Unlock()
	for _, p := range gone {
		if err := s.DropProject(p); err != nil {
			return err
		}
	}
	return nil
}

// Connections lists connections, oldest first (copies).
func (s *Store) Connections() []Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Connection, 0, len(s.conns))
	for _, c := range s.conns {
		cc := *c
		cc.Projects = slices.Clone(c.Projects)
		cc.RedirectURIs = slices.Clone(c.RedirectURIs)
		out = append(out, cc)
	}
	slices.SortFunc(out, func(a, b Connection) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Authenticate resolves a bearer credential to its connection. It accepts
// static client tokens and OAuth access tokens; resource is the canonical
// URL of the endpoint being called, which an access token must be bound to.
// Every failure returns nil, so callers can't tell them apart.
func (s *Store) Authenticate(bearer, resource string) *Connection {
	h := hashSecret(bearer)
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	switch {
	case strings.HasPrefix(bearer, prefixClientToken):
		id = s.secrets[h]
		if c := s.conns[id]; c == nil || c.Kind != KindToken {
			return nil
		}
	case strings.HasPrefix(bearer, prefixAccessToken):
		t := s.access[h]
		if t == nil || s.now().After(t.ExpiresAt) || t.Resource != resource {
			return nil
		}
		id = t.ConnectionID
	default:
		return nil
	}
	c := s.conns[id]
	if c == nil {
		return nil
	}
	c.LastUsedAt = s.now().UTC()
	cc := *c
	cc.Projects = slices.Clone(c.Projects)
	return &cc
}

// Connection returns a connection by id (a copy).
func (s *Store) Connection(id string) *Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conns[id]
	if c == nil {
		return nil
	}
	cc := *c
	cc.Projects = slices.Clone(c.Projects)
	cc.RedirectURIs = slices.Clone(c.RedirectURIs)
	return &cc
}

// oauthClient returns the OAuth connection (pre-registered or approved
// app) for a client id.
func (s *Store) oauthClient(clientID string) *Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.conns[s.clients[clientID]]
	if c == nil || (c.Kind != KindOAuth && c.Kind != KindApp) {
		return nil
	}
	cc := *c
	cc.Projects = slices.Clone(c.Projects)
	cc.RedirectURIs = slices.Clone(c.RedirectURIs)
	return &cc
}

// authenticateClient checks an OAuth client at the token endpoint: a
// confidential client must present its secret, and a public client must
// present none (it is held to PKCE instead).
func (s *Store) authenticateClient(clientID, secret string) *Connection {
	if clientID == "" {
		return nil
	}
	c := s.oauthClient(clientID)
	if c == nil {
		return nil
	}
	if c.Public() {
		if secret != "" {
			return nil
		}
		return c
	}
	if !strings.HasPrefix(secret, prefixClientSecret) || subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(c.SecretHash)) != 1 {
		return nil
	}
	return c
}

// OpenPairing opens a window for one app to register and be approved.
func (s *Store) OpenPairing(rawName string, projects []string, scope middleware.Scope, knownProject func(string) bool) (*Pairing, error) {
	name, projects, err := validateGrant(rawName, projects, scope, knownProject)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(s.pairings) >= 5 {
		return nil, errors.New("too many open pairings; wait for them to expire or cancel one")
	}
	p := &Pairing{ID: "pair_" + randomHex(8), Name: name, Projects: projects, Scope: scope, ExpiresAt: s.now().Add(pairingTTL)}
	s.pairings[p.ID] = p
	cp := *p
	return &cp, nil
}

// CancelPairing closes a pairing.
func (s *Store) CancelPairing(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pairings, id)
}

// Pairings lists open pairings.
func (s *Store) Pairings() []Pairing {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	out := make([]Pairing, 0, len(s.pairings))
	for _, p := range s.pairings {
		cp := *p
		cp.Projects = slices.Clone(p.Projects)
		out = append(out, cp)
	}
	slices.SortFunc(out, func(a, b Pairing) int { return a.ExpiresAt.Compare(b.ExpiresAt) })
	return out
}

// registerClient records a dynamically registered client. Registration is
// only open while the owner has a pairing open.
func (s *Store) registerClient(name string, redirectURIs []string, confidential bool) (clientID, secret string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(s.pairings) == 0 {
		return "", "", errors.New("registration is closed: open a pairing in the towline gateway page first")
	}
	if len(s.pending) >= maxPendingClients {
		return "", "", errors.New("too many clients waiting for approval")
	}
	pc := &pendingClient{
		ClientID:     prefixClientID + randomHex(16),
		ClientName:   name,
		RedirectURIs: slices.Clone(redirectURIs),
		ExpiresAt:    s.now().Add(pairingTTL),
	}
	if confidential {
		secret = prefixClientSecret + randomHex(32)
		pc.SecretHash = hashSecret(secret)
	}
	s.pending[pc.ClientID] = pc
	return pc.ClientID, secret, nil
}

// pendingClient returns a registered client awaiting consent.
func (s *Store) pendingClientByID(clientID string) *pendingClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	pc := s.pending[clientID]
	if pc == nil {
		return nil
	}
	cp := *pc
	cp.RedirectURIs = slices.Clone(pc.RedirectURIs)
	return &cp
}

// approveClient binds a registered client to a pairing, creating an app
// connection with the pairing's projects and scope. Both are consumed.
func (s *Store) approveClient(clientID, pairingID string) (*Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	pc := s.pending[clientID]
	p := s.pairings[pairingID]
	if pc == nil {
		return nil, errors.New("this app's registration expired; add the connector again")
	}
	if p == nil {
		return nil, errors.New("the pairing expired; open a new one in the towline gateway page")
	}
	if len(s.conns) >= maxConnections {
		return nil, errors.New("too many connections; revoke some first")
	}
	c := &Connection{
		ID:           "conn_" + randomHex(8),
		Name:         p.Name,
		Kind:         KindApp,
		Projects:     slices.Clone(p.Projects),
		Scope:        p.Scope,
		SecretHash:   pc.SecretHash,
		ClientID:     pc.ClientID,
		ClientName:   pc.ClientName,
		RedirectURIs: slices.Clone(pc.RedirectURIs),
		CreatedAt:    s.now().UTC(),
	}
	s.conns[c.ID] = c
	if c.SecretHash != "" {
		s.secrets[c.SecretHash] = c.ID
	}
	s.clients[c.ClientID] = c.ID
	delete(s.pending, clientID)
	delete(s.pairings, pairingID)
	if err := s.saveLocked(); err != nil {
		s.deleteLocked(c.ID)
		return nil, err
	}
	cc := *c
	return &cc, nil
}

// issueCode stores a new authorization code.
func (s *Store) issueCode(c *Connection, redirectURI string, redirectGiven bool, resource, challenge string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	n := 0
	for _, ac := range s.codes {
		if ac.ConnectionID == c.ID {
			n++
		}
	}
	if n >= maxCodesPerClient {
		return "", errors.New("too many pending authorization requests for this client")
	}
	code := prefixAuthCode + randomHex(32)
	s.codes[hashSecret(code)] = &authCode{
		ConnectionID:  c.ID,
		ClientID:      c.ClientID,
		RedirectURI:   redirectURI,
		RedirectGiven: redirectGiven,
		Resource:      resource,
		CodeChallenge: challenge,
		ExpiresAt:     s.now().Add(authCodeTTL),
	}
	return code, nil
}

// tokenPair is an issued access (and optionally refresh) token.
type tokenPair struct {
	Access  string
	Refresh string
}

// redeemCode exchanges an authorization code. A code is single use: a
// replay revokes the tokens the first redemption issued.
func (s *Store) redeemCode(c *Connection, code, redirectURI, verifier, resource string) (*tokenPair, string, error) {
	h := hashSecret(code)
	s.mu.Lock()
	defer s.mu.Unlock()
	ac := s.codes[h]
	if ac == nil || s.now().After(ac.ExpiresAt) {
		return nil, "", errors.New("invalid or expired authorization code")
	}
	if ac.Used {
		s.revokeCodeTokensLocked(h)
		_ = s.saveLocked()
		return nil, "", errors.New("authorization code was already used")
	}
	if ac.ConnectionID != c.ID || ac.ClientID != c.ClientID {
		return nil, "", errors.New("authorization code was issued to another client")
	}
	if (ac.RedirectGiven || redirectURI != "") && ac.RedirectURI != redirectURI {
		return nil, "", errors.New("redirect_uri does not match the authorization request")
	}
	if resource != "" && resource != ac.Resource {
		return nil, "", errors.New("resource does not match the authorization request")
	}
	if !verifyPKCE(verifier, ac.CodeChallenge) {
		return nil, "", errors.New("PKCE verification failed")
	}
	ac.Used = true
	pair, err := s.issueTokensLocked(c.ID, ac.Resource, h, true)
	if err != nil {
		return nil, "", err
	}
	return pair, ac.Resource, nil
}

func (s *Store) revokeCodeTokensLocked(codeHash string) {
	if codeHash == "" {
		return
	}
	for th, t := range s.access {
		if t.CodeHash == codeHash {
			delete(s.access, th)
		}
	}
	for th, t := range s.refresh {
		if t.CodeHash == codeHash {
			delete(s.refresh, th)
		}
	}
}

// refreshAccess issues a new access token from a refresh token. Refresh
// tokens belong to confidential clients (the secret is checked on every
// refresh), so they are not rotated: rotation would revoke connectors
// that retry a refresh after a network error.
func (s *Store) refreshAccess(c *Connection, refreshToken, resource string) (*tokenPair, string, error) {
	h := hashSecret(refreshToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.refresh[h]
	if !strings.HasPrefix(refreshToken, prefixRefreshToken) || t == nil || s.now().After(t.ExpiresAt) || t.ConnectionID != c.ID {
		return nil, "", errors.New("invalid or expired refresh token")
	}
	if resource != "" && resource != t.Resource {
		return nil, "", errors.New("resource does not match the grant")
	}
	// A retry within the grace window may have produced several
	// successors. Once any of them has been used (or is gone), the old
	// token turning up again means it was copied.
	for _, sh := range t.Successors {
		if succ := s.refresh[sh]; succ == nil || succ.Used {
			s.revokeCodeTokensLocked(t.CodeHash)
			_ = s.saveLocked()
			return nil, "", errors.New("refresh token was already rotated; the grant is revoked")
		}
	}
	t.Used = true
	// Public clients get a new refresh token each time (OAuth 2.1). The old
	// one keeps working briefly, so a retried refresh doesn't log the app
	// out.
	rotate := c.Public()
	if rotate {
		if grace := s.now().Add(refreshGrace); t.ExpiresAt.After(grace) {
			t.ExpiresAt = grace
		}
	}
	pair, err := s.issueTokensLocked(c.ID, t.Resource, t.CodeHash, rotate)
	if err != nil {
		return nil, "", err
	}
	if rotate {
		t.Successors = append(t.Successors, hashSecret(pair.Refresh))
		_ = s.saveLocked()
	}
	return pair, t.Resource, nil
}

func (s *Store) issueTokensLocked(connID, resource, codeHash string, withRefresh bool) (*tokenPair, error) {
	s.pruneLocked()
	n := 0
	for _, t := range s.access {
		if t.ConnectionID == connID {
			n++
		}
	}
	if n >= maxTokensPerConnection {
		// Drop the oldest access token rather than refusing: a client that
		// refreshes often must keep working.
		var oldest string
		for h, t := range s.access {
			if t.ConnectionID == connID && (oldest == "" || t.ExpiresAt.Before(s.access[oldest].ExpiresAt)) {
				oldest = h
			}
		}
		delete(s.access, oldest)
	}
	now := s.now()
	pair := &tokenPair{Access: prefixAccessToken + randomHex(32)}
	s.access[hashSecret(pair.Access)] = &oauthToken{ConnectionID: connID, Resource: resource, ExpiresAt: now.Add(accessTokenTTL), CodeHash: codeHash}
	if withRefresh {
		r := 0
		for _, t := range s.refresh {
			if t.ConnectionID == connID {
				r++
			}
		}
		if r >= maxTokensPerConnection {
			var oldest string
			for h, t := range s.refresh {
				if t.ConnectionID == connID && (oldest == "" || t.ExpiresAt.Before(s.refresh[oldest].ExpiresAt)) {
					oldest = h
				}
			}
			delete(s.refresh, oldest)
		}
		pair.Refresh = prefixRefreshToken + randomHex(32)
		s.refresh[hashSecret(pair.Refresh)] = &oauthToken{ConnectionID: connID, Resource: resource, ExpiresAt: now.Add(refreshTokenTTL), CodeHash: codeHash}
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return pair, nil
}

// revokeToken deletes an access or refresh token belonging to c. Unknown
// tokens are ignored (RFC 7009).
func (s *Store) revokeToken(c *Connection, token string) {
	h := hashSecret(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.access[h]; t != nil && t.ConnectionID == c.ID {
		delete(s.access, h)
	}
	if t := s.refresh[h]; t != nil && t.ConnectionID == c.ID {
		delete(s.refresh, h)
	}
	_ = s.saveLocked()
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
