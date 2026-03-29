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

	return nil
}
