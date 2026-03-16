package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadGlobalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &GlobalConfig{
		PortainerURL:    "https://portainer.example.com:9443",
		PortainerAPIKey: "ptr_supersecrettoken123",
		PortainerEnvID:  42,
		ProjectsDir:     "/home/user/projects",
	}

	err := SaveGlobalConfig(cfg)
	require.NoError(t, err)

	loaded, err := LoadGlobalConfig()
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, cfg.PortainerURL, loaded.PortainerURL)
	assert.Equal(t, cfg.PortainerAPIKey, loaded.PortainerAPIKey)
	assert.Equal(t, cfg.PortainerEnvID, loaded.PortainerEnvID)
	assert.Equal(t, cfg.ProjectsDir, loaded.ProjectsDir)
}

func TestLoadGlobalConfig_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	_, err := LoadGlobalConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "towline not configured")
}

func TestSaveAndLoadProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &ProjectConfig{
		StackName: "myproject-dev",
		StackID:   7,
		Tier:      "dev",
		TeamID:    3,
		UserID:    12,
		EnvID:     1,
		MCPBinary: "/usr/local/bin/towline-mcp",
		MCPArgs: MCPArgs{
			Server: "https://portainer.example.com:9443",
			Token:  "ptr_token_abc",
			Stack:  "myproject-dev",
			Tier:   "dev",
		},
	}

	err := SaveProjectConfig(tmpDir, cfg)
	require.NoError(t, err)

	loaded, err := LoadProjectConfig(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, cfg.StackName, loaded.StackName)
	assert.Equal(t, cfg.StackID, loaded.StackID)
	assert.Equal(t, cfg.Tier, loaded.Tier)
	assert.Equal(t, cfg.TeamID, loaded.TeamID)
	assert.Equal(t, cfg.UserID, loaded.UserID)
	assert.Equal(t, cfg.EnvID, loaded.EnvID)
	assert.Equal(t, cfg.MCPBinary, loaded.MCPBinary)
	assert.Equal(t, cfg.MCPArgs.Server, loaded.MCPArgs.Server)
	assert.Equal(t, cfg.MCPArgs.Token, loaded.MCPArgs.Token)
	assert.Equal(t, cfg.MCPArgs.Stack, loaded.MCPArgs.Stack)
	assert.Equal(t, cfg.MCPArgs.Tier, loaded.MCPArgs.Tier)
}

func TestGlobalConfigPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &GlobalConfig{
		PortainerURL:    "https://portainer.example.com:9443",
		PortainerAPIKey: "ptr_secret",
		PortainerEnvID:  1,
		ProjectsDir:     "/tmp/projects",
	}

	err := SaveGlobalConfig(cfg)
	require.NoError(t, err)

	cfgPath := filepath.Join(tmpDir, ".towline", "config.yaml")
	info, err := os.Stat(cfgPath)
	require.NoError(t, err)

	// Verify file permissions are 0600 (owner read/write only)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestLoadProjectConfig_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := LoadProjectConfig(tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no towline.json found")
}
