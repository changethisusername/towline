package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GlobalConfig holds the towline global configuration stored at ~/.towline/config.yaml.
type GlobalConfig struct {
	PortainerURL    string `yaml:"portainer_url"`
	PortainerAPIKey string `yaml:"portainer_admin_key"`
	PortainerEnvID  int    `yaml:"portainer_env_id"`
	ProjectsDir     string `yaml:"projects_dir"`
	// SkipTLSVerify disables certificate verification for a self-signed
	// Portainer certificate. Unset means the config predates this option;
	// see InsecureTLS.
	SkipTLSVerify *bool `yaml:"skip_tls_verify,omitempty"`
	// Approval configures how prod-tier operations are approved.
	Approval ApprovalConfig `yaml:"approval,omitempty"`
}

// Approval modes (see middleware.ApprovalMode).
const (
	ApprovalModeHuman = "human"
	ApprovalModeAgent = "agent"
)

// ApprovalConfig configures prod-tier approvals.
type ApprovalConfig struct {
	// Mode is "human" (approval server) or "agent" (the agent confirms its
	// own operations). Empty means the config predates approval modes and
	// behaves as "agent", which is how earlier releases worked.
	Mode string `yaml:"mode,omitempty"`
	// URL is the approval server towline-mcp talks to (the built-in server
	// or an external one implementing the same contract).
	URL string `yaml:"url,omitempty"`
	// WebhookToken authenticates towline-mcp to the approval server. It is
	// written into agent configs, so it can only submit and poll requests.
	WebhookToken string `yaml:"webhook_token,omitempty"`
	// Listen is the built-in server's listen address.
	Listen string `yaml:"listen,omitempty"`
	// ApproverTokenHash is the SHA-256 of the human approver token.
	ApproverTokenHash string `yaml:"approver_token_hash,omitempty"`
	// NtfyURL optionally receives a push notification per request.
	NtfyURL string `yaml:"ntfy_url,omitempty"`
	// PublicURL is where humans reach the approval UI (for notification links).
	PublicURL string `yaml:"public_url,omitempty"`
}

// EffectiveMode returns the approval mode, treating an unset mode as agent
// confirmation for compatibility with configs from earlier releases.
func (a ApprovalConfig) EffectiveMode() string {
	if a.Mode == "" {
		return ApprovalModeAgent
	}
	return a.Mode
}

// InsecureTLS reports whether to skip TLS verification. Configs written
// before skip_tls_verify existed always skipped verification, so an unset
// value keeps doing that (legacy=true) until the user re-runs setup or sets
// skip_tls_verify explicitly.
func (c *GlobalConfig) InsecureTLS() (skip, legacy bool) {
	if c.SkipTLSVerify == nil {
		return true, true
	}
	return *c.SkipTLSVerify, false
}

// ProjectConfig holds per-project configuration stored at towline.json in the project root.
type ProjectConfig struct {
	StackName string  `json:"stack_name"`
	StackID   int     `json:"stack_id"`
	Tier      string  `json:"tier"`
	TeamID    int     `json:"team_id"`
	UserID    int     `json:"user_id"`
	EnvID     int     `json:"environment_id"`
	MCPBinary string  `json:"mcp_binary"`
	MCPArgs   MCPArgs `json:"mcp_args"`
	// ComposePolicy relaxes the compose security policy for this project.
	// Edit it (as the human operator) and run 'towline refresh' to apply.
	ComposePolicy *ComposePolicyConfig `json:"compose_policy,omitempty"`
}

// ComposePolicyConfig is a project's compose security policy.
type ComposePolicyConfig struct {
	// Mode is "enforce" (default) or "off".
	Mode string `json:"mode,omitempty"`
	// AllowBindMounts lists host paths stacks may bind mount; "." allows
	// relative paths inside the stack directory.
	AllowBindMounts []string `json:"allow_bind_mounts,omitempty"`
}

// MCPArgs holds the arguments passed to the towline-mcp binary.
type MCPArgs struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	Stack  string `json:"stack"`
	Tier   string `json:"tier"`
}

// GlobalConfigPath returns the path to the global towline config file (~/.towline/config.yaml).
func GlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".towline", "config.yaml"), nil
}

// LoadGlobalConfig reads and parses the global towline configuration.
func LoadGlobalConfig() (*GlobalConfig, error) {
	cfgPath, err := GlobalConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("towline not configured — run 'towline setup' first")
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if cfg.PortainerURL == "" || cfg.PortainerAPIKey == "" {
		return nil, fmt.Errorf("towline not configured — run 'towline setup' first")
	}

	return &cfg, nil
}

// SaveGlobalConfig writes the global towline configuration with restrictive permissions.
func SaveGlobalConfig(cfg *GlobalConfig) error {
	cfgPath, err := GlobalConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	// WriteFile keeps the mode of an existing file; the config holds the
	// admin key, so tighten it explicitly.
	if err := os.Chmod(cfgPath, 0600); err != nil {
		return fmt.Errorf("failed to set config permissions: %w", err)
	}

	return nil
}

// LoadProjectConfig reads and parses a project's towline.json configuration.
func LoadProjectConfig(projectDir string) (*ProjectConfig, error) {
	cfgPath := filepath.Join(projectDir, "towline.json")

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no towline.json found in %s — not a towline project", projectDir)
		}
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse project config: %w", err)
	}

	return &cfg, nil
}

// SaveProjectConfig writes a project's towline.json configuration.
func SaveProjectConfig(projectDir string, cfg *ProjectConfig) error {
	cfgPath := filepath.Join(projectDir, "towline.json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}
	if err := os.Chmod(cfgPath, 0600); err != nil {
		return fmt.Errorf("failed to set project config permissions: %w", err)
	}

	return nil
}
