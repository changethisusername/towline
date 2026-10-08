package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Version is the gateway version, set by the binary.
var Version = "dev"

const (
	// maxBodyBytes bounds every request body. Compose files are the
	// largest legitimate payload.
	maxBodyBytes = 2 << 20
	// maxFormBytes bounds form and OAuth request bodies.
	maxFormBytes = 64 << 10
)

// Gateway serves remote MCP clients.
type Gateway struct {
	cfg       *Config
	store     *Store
	approvals *approval.Server
	mcp       *mcpRouter
	ui        *ownerUI
	limiter   *rateLimiter
	audit     zerolog.Logger
	publicURL *url.URL
	now       func() time.Time

	authzMu  sync.Mutex
	authzReq map[string]*authzRequest
}

// Options configures New.
type Options struct {
	// Builder creates each project's MCP server; nil uses the real one.
	Builder ProjectBuilder
	// Audit receives audit events; nil logs to stdout.
	Audit *zerolog.Logger
}

// New creates a gateway from a validated config.
func New(cfg *Config, opts Options) (*Gateway, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}
	store, err := OpenStore(filepath.Join(cfg.DataDir, "state.json"))
	if err != nil {
		return nil, err
	}
	// A project removed from the config loses its connections for good, so
	// adding a project with the same name later grants nothing old.
	if err := store.DropProjectsExcept(cfg.ProjectNames()); err != nil {
		return nil, err
	}
	// The approval server is used in-process only: its webhook token is
	// random and never leaves this process, and its HTTP handler is not
	// mounted. Humans decide in the owner UI.
	approvals, err := approval.NewServer(approval.ServerConfig{
		WebhookToken:      approval.NewToken(),
		ApproverTokenHash: cfg.OwnerTokenHash,
		NtfyURL:           cfg.NtfyURL,
		PublicURL:         cfg.PublicURL, // the approval server adds /ui
	})
	if err != nil {
		return nil, err
	}
	pu, _ := url.Parse(cfg.PublicURL)
	g := &Gateway{
		cfg:       cfg,
		store:     store,
		approvals: approvals,
		limiter:   newRateLimiter(),
		publicURL: pu,
		now:       time.Now,
		authzReq:  map[string]*authzRequest{},
	}
	if opts.Audit != nil {
		g.audit = *opts.Audit
	} else {
		g.audit = zerolog.New(os.Stdout).With().Timestamp().Str("log", "audit").Logger()
	}
	builder := opts.Builder
	if builder == nil {
		builder = buildProject
	}
	router, err := newMCPRouter(g, builder)
	if err != nil {
		return nil, err
	}
	g.mcp = router
	g.ui = newOwnerUI(g)
	return g, nil
}

// Close stops background work.
func (g *Gateway) Close() {
	g.mcp.close()
}

// Handler returns the gateway's HTTP handler.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// MCP endpoints
	mux.Handle("/mcp", g.mcp.handler(""))
	mux.HandleFunc("/p/{project}/mcp", func(w http.ResponseWriter, r *http.Request) {
		g.mcp.handler(r.PathValue("project")).ServeHTTP(w, r)
	})

	// OAuth discovery and endpoints
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", g.handleResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", g.handleResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/p/{project}/mcp", g.handleResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", g.handleServerMetadata)
	mux.HandleFunc("GET /.well-known/openid-configuration", g.handleServerMetadata)
	mux.HandleFunc("POST /oauth/register", g.handleRegister)
	mux.HandleFunc("GET /oauth/authorize", g.handleAuthorize)
	mux.HandleFunc("POST /oauth/authorize", g.handleConsent)
	mux.HandleFunc("POST /oauth/token", g.handleToken)
	mux.HandleFunc("POST /oauth/revoke", g.handleRevoke)

	// Owner UI
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusSeeOther)
	})
	g.ui.register(mux)

	return g.withDefenses(mux)
}

// withDefenses applies the checks every request goes through.
func (g *Gateway) withDefenses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// same-origin, not no-referrer: with no-referrer browsers send
		// "Origin: null" on form posts, which the origin check refuses.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if g.publicURL.Scheme == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}

		// Health checks come from inside the container network with any
		// Host; everything else must be addressed to the public host, so a
		// DNS-rebinding page can't talk to the gateway.
		if r.URL.Path != "/healthz" && !g.hostAllowed(r.Host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func (g *Gateway) hostAllowed(host string) bool {
	return strings.EqualFold(host, g.publicURL.Host) ||
		(g.publicURL.Port() == "" && strings.EqualFold(strings.TrimSuffix(host, defaultPort(g.publicURL.Scheme)), g.publicURL.Host))
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return ":443"
	}
	return ":80"
}

// clientIP is the address used for rate limiting. Behind cloudflared the
// TCP peer is always the tunnel, so the edge's CF-Connecting-IP is used
// when the config says every request comes through Cloudflare.
func (g *Gateway) clientIP(r *http.Request) string {
	if g.cfg.TrustCloudflare {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin checks a browser form post came from the gateway's own pages.
func (g *Gateway) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	site := r.Header.Get("Sec-Fetch-Site")
	switch origin {
	case "":
		return site == "" || site == "same-origin" || site == "none"
	case "null":
		// Some privacy settings still null the Origin; Fetch Metadata
		// tells us where the request came from.
		return site == "same-origin"
	}
	return origin == g.cfg.PublicURL
}

// originAllowed checks the Origin of an MCP request: absent (server-side
// clients) or explicitly allow-listed.
func (g *Gateway) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || slices.Contains(g.cfg.AllowedOrigins, origin)
}

// isOwnerToken checks the owner token in constant time.
func (g *Gateway) isOwnerToken(token string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(approval.HashToken(token)), []byte(g.cfg.OwnerTokenHash)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// --- Rate limiting ---

// rateLimiter is a fixed-window counter per key.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
	now     func() time.Time
	last    time.Time
}

type window struct {
	start time.Time
	count int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windows: map[string]*window{}, now: time.Now}
}

// allow counts one event for key and reports whether it is within limit
// events per period.
func (l *rateLimiter) allow(key string, limit int, period time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.last) > time.Minute {
		for k, w := range l.windows {
			if now.Sub(w.start) > time.Hour {
				delete(l.windows, k)
			}
		}
		l.last = now
	}
	w := l.windows[key]
	if w == nil || now.Sub(w.start) >= period {
		w = &window{start: now}
		l.windows[key] = w
	}
	w.count++
	return w.count <= limit
}

// peek reports whether key is currently over limit without counting.
func (l *rateLimiter) peek(key string, limit int, period time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[key]
	return w == nil || l.now().Sub(w.start) >= period || w.count < limit
}

// Limits.
const (
	// unauthenticated OAuth and registration requests per IP
	limitOAuthPerIP = 60
	// failed owner-token attempts per IP
	limitLoginFailPerIP = 10
	// dynamic client registrations per IP (per 10 minutes)
	limitRegisterPerIP = 5
	// MCP requests per connection
	limitMCPPerConnection = 600
	// failed MCP authentications per IP
	limitAuthFailPerIP = 60
)

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

// --- Entry point ---

// Main runs "towline-mcp gateway [flags]".
func Main(args []string) error {
	if len(args) > 0 && args[0] == "healthcheck" {
		return healthcheck(args[1:])
	}
	fs := newFlagSet()
	configPath := fs.String("config", "", "Path to the gateway config (default: the "+ConfigEnvVar+" environment variable)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	g, err := New(cfg, Options{})
	if err != nil {
		return err
	}
	defer g.Close()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           g.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	log.Info().Str("version", Version).Str("listen", cfg.Listen).Str("public_url", cfg.PublicURL).
		Strs("projects", cfg.ProjectNames()).Msg("towline gateway listening")

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sig:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

// healthcheck probes the local /healthz (for the container healthcheck;
// the image has no shell or curl).
func healthcheck(args []string) error {
	addr := "127.0.0.1:8080"
	if len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			fmt.Println("usage: towline-mcp gateway healthcheck [host:port]")
			return nil
		}
		addr = args[0]
	} else if cfg, err := LoadConfig(""); err == nil {
		// Follow a non-default listen address from the config.
		if _, port, err := net.SplitHostPort(cfg.Listen); err == nil && port != "" {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
