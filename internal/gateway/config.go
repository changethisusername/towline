// Package gateway is the remote towline MCP gateway: an HTTP server that
// exposes towline projects to remote MCP clients (Claude connectors,
// Claude Code, Cursor, ...) through scoped, owner-issued connections.
//
// Each project gets the same towline MCP server the stdio binary runs, with
// the same middleware chain and its own team-scoped Portainer token. The
// gateway adds authentication (static bearer tokens or OAuth 2.1 for
// pre-registered clients), per-connection project and scope checks, human
// approvals in an owner UI, rate limits and an audit log.
package gateway

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/changethisusername/towline/internal/middleware"
	"gopkg.in/yaml.v3"
)

// ConfigEnvVar holds the gateway config (YAML, or "base64:" + base64 YAML)
// when no -config file is given. Deploying the gateway as a Portainer stack
// passes it this way, since a stack has no files of its own.
const ConfigEnvVar = "TOWLINE_GATEWAY_CONFIG"

// projectNamePattern limits project names so that "<project>__<tool>" tool
// names stay within the 64-character limit MCP clients enforce, and so the
// "__" separator can never appear inside a project name.
var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Config is the gateway configuration, written by 'towline remote'.
type Config struct {
	// PublicURL is the origin clients use, e.g. https://mcp.example.com.
	PublicURL string `yaml:"public_url"`
	// Listen is the address to listen on (default :8080).
	Listen string `yaml:"listen,omitempty"`
	// DataDir holds the state file and tool definitions (default /data).
	DataDir string `yaml:"data_dir,omitempty"`
	// OwnerTokenHash is the hex SHA-256 of the owner token, which logs in
	// to the owner UI (connections and approvals).
	OwnerTokenHash string `yaml:"owner_token_hash"`
	// TrustCloudflare honors CF-Connecting-IP as the client address. Only
	// set it when every request arrives through cloudflared.
	TrustCloudflare bool `yaml:"trust_cloudflare,omitempty"`
	// AllowedOrigins lists browser origins allowed to call the MCP
	// endpoints. Server-side clients send no Origin; default none.
	AllowedOrigins []string `yaml:"allowed_origins,omitempty"`
	// NtfyURL, if set, receives a push notification for each approval
	// request (action, project and short id only).
	NtfyURL string `yaml:"ntfy_url,omitempty"`

	Portainer PortainerConfig `yaml:"portainer"`
	Projects  []ProjectConfig `yaml:"projects"`
}

// PortainerConfig says how the gateway reaches Portainer.
type PortainerConfig struct {
	URL           string `yaml:"url"`
	SkipTLSVerify bool   `yaml:"skip_tls_verify,omitempty"`
}

// Approval policies for changes made through the gateway.
const (
	// ApprovalAlways needs a human approval for configuration, deploy,
	// destructive and exec operations on every tier (the default).
	ApprovalAlways = "always"
	// ApprovalProd needs approval on prod only, like the stdio server.
	ApprovalProd = "prod"
)

// ProjectConfig is one project the gateway serves.
type ProjectConfig struct {
	// Name is how connections and tool names refer to the project.
	Name  string `yaml:"name"`
	Stack string `yaml:"stack"`
	Tier  string `yaml:"tier"`
	// Token is the project's team-scoped Portainer API token.
	Token string `yaml:"token"`
	// Approval is "always" (default) or "prod".
	Approval      string              `yaml:"approval,omitempty"`
	ComposePolicy ComposePolicyConfig `yaml:"compose_policy,omitempty"`
	Proxy         string              `yaml:"proxy,omitempty"`
	CaddyAPI      string              `yaml:"caddy_api,omitempty"`
}

// ComposePolicyConfig mirrors the project's compose policy.
type ComposePolicyConfig struct {
	Mode            string   `yaml:"mode,omitempty"`
	AllowBindMounts []string `yaml:"allow_bind_mounts,omitempty"`
	AllowNetworks   []string `yaml:"allow_networks,omitempty"`
	AllowVolumes    []string `yaml:"allow_volumes,omitempty"`
}

// Policy converts the config into the middleware policy.
func (c ComposePolicyConfig) Policy(stack string) middleware.ComposePolicy {
	return middleware.ComposePolicy{
		StackName:       stack,
		Disabled:        c.Mode == "off",
		AllowBindMounts: c.AllowBindMounts,
		AllowNetworks:   c.AllowNetworks,
		AllowVolumes:    c.AllowVolumes,
	}
}

// LoadConfig reads the config from path, or from ConfigEnvVar if path is
// empty.
func LoadConfig(path string) (*Config, error) {
	var data []byte
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
		data = b
	} else {
		v := os.Getenv(ConfigEnvVar)
		if v == "" {
			return nil, fmt.Errorf("no config: pass -config or set %s", ConfigEnvVar)
		}
		if enc, ok := strings.CutPrefix(v, "base64:"); ok {
			b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
			if err != nil {
				return nil, fmt.Errorf("failed to decode %s: %w", ConfigEnvVar, err)
			}
			data = b
		} else {
			data = []byte(v)
		}
	}
	return ParseConfig(data)
}

// ParseConfig parses and validates a YAML config.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the config and fills in defaults. It fails closed: a
// config the gateway cannot serve safely is refused at startup.
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "/data"
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("public_url must be an origin like https://mcp.example.com, got %q", c.PublicURL)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Plain HTTP only for local testing: tokens would cross the
		// network in the clear.
		if !isLoopbackHost(u.Hostname()) {
			return errors.New("public_url must use https (http is only allowed for localhost)")
		}
	default:
		return fmt.Errorf("public_url must use https, got %q", u.Scheme)
	}
	c.PublicURL = u.Scheme + "://" + u.Host

	if len(c.OwnerTokenHash) != 64 || strings.Trim(strings.ToLower(c.OwnerTokenHash), "0123456789abcdef") != "" {
		return errors.New("owner_token_hash must be the hex SHA-256 of the owner token")
	}
	c.OwnerTokenHash = strings.ToLower(c.OwnerTokenHash)
	for _, o := range c.AllowedOrigins {
		if ou, err := url.Parse(o); err != nil || ou.Host == "" || ou.Path != "" || (ou.Scheme != "https" && ou.Scheme != "http") {
			return fmt.Errorf("allowed_origins entry %q is not an origin", o)
		}
	}
	if c.NtfyURL != "" {
		if nu, err := url.Parse(c.NtfyURL); err != nil || nu.Scheme != "https" || nu.Host == "" {
			return errors.New("ntfy_url must be an https URL")
		}
	}

	if pu, err := url.Parse(c.Portainer.URL); err != nil || pu.Host == "" || (pu.Scheme != "https" && pu.Scheme != "http") {
		return errors.New("portainer.url must be an http(s) URL")
	}

	if len(c.Projects) == 0 {
		return errors.New("no projects configured")
	}
	seen := map[string]bool{}
	for i := range c.Projects {
		p := &c.Projects[i]
		if !projectNamePattern.MatchString(p.Name) {
			return fmt.Errorf("project name %q must be 1-32 lowercase letters, digits or dashes", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("project %q is listed twice", p.Name)
		}
		seen[p.Name] = true
		if p.Stack == "" {
			p.Stack = p.Name
		}
		if p.Tier != string(middleware.TierDev) && p.Tier != string(middleware.TierProd) {
			return fmt.Errorf("project %q: tier must be dev or prod", p.Name)
		}
		// towline names stacks <project>-<tier>; a stack whose name says
		// prod is never served as dev.
		if strings.HasSuffix(p.Stack, "-prod") && p.Tier != string(middleware.TierProd) {
			return fmt.Errorf("project %q: stack %q is a prod stack but tier is %q", p.Name, p.Stack, p.Tier)
		}
		if p.Token == "" {
			return fmt.Errorf("project %q: token is required", p.Name)
		}
		switch p.Approval {
		case "":
			p.Approval = ApprovalAlways
		case ApprovalAlways, ApprovalProd:
		default:
			return fmt.Errorf("project %q: approval must be %q or %q", p.Name, ApprovalAlways, ApprovalProd)
		}
		switch p.ComposePolicy.Mode {
		case "", "enforce", "off":
		default:
			return fmt.Errorf("project %q: compose_policy.mode must be enforce or off", p.Name)
		}
		switch p.Proxy {
		case "", "traefik", "caddy", "cloudflare":
		default:
			return fmt.Errorf("project %q: proxy must be traefik, caddy or cloudflare", p.Name)
		}
	}
	return nil
}

// Project returns the named project.
func (c *Config) Project(name string) (*ProjectConfig, bool) {
	for i := range c.Projects {
		if c.Projects[i].Name == name {
			return &c.Projects[i], true
		}
	}
	return nil, false
}

// ProjectNames lists the configured projects in config order.
func (c *Config) ProjectNames() []string {
	names := make([]string, len(c.Projects))
	for i, p := range c.Projects {
		names[i] = p.Name
	}
	return names
}

// ValidProjectName reports whether name is a well-formed project name.
func ValidProjectName(name string) bool {
	return projectNamePattern.MatchString(name)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
