package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	towline "github.com/changethisusername/towline"
	"gopkg.in/yaml.v3"
)

// TemplateData holds the variables available to all templates.
type TemplateData struct {
	ProjectName   string
	StackName     string
	Tier          string
	PortainerURL  string
	EnvironmentID int
	APIToken      string
	MCPBinaryPath string
	TeamID        int
	UserID        int
}

// renderTemplate renders a named template to outputPath with the given data.
// It checks ~/.towline/templates/ first for user overrides, then falls back
// to embedded templates.
func renderTemplate(name string, data TemplateData, outputPath string) error {
	home, _ := os.UserHomeDir()
	userPath := filepath.Join(home, ".towline", "templates", name)

	var tmplContent []byte
	var err error

	if _, statErr := os.Stat(userPath); statErr == nil {
		tmplContent, err = os.ReadFile(userPath)
	} else {
		tmplContent, err = towline.EmbeddedTemplates.ReadFile("templates/" + name)
	}
	if err != nil {
		return fmt.Errorf("template '%s' not found: %w", name, err)
	}

	tmpl, err := template.New(name).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return tmpl.Execute(f, data)
}

// copySkillFile copies the DevOps skill file into the project directory.
// It checks ~/.towline/skills/ first for user overrides, then falls back
// to the embedded skill file.
func copySkillFile(projectDir string) error {
	home, _ := os.UserHomeDir()
	userPath := filepath.Join(home, ".towline", "skills", "towline-devops.md")

	var content []byte
	var err error

	if _, statErr := os.Stat(userPath); statErr == nil {
		content, err = os.ReadFile(userPath)
	} else {
		content, err = towline.EmbeddedSkills.ReadFile("skills/towline-devops.md")
	}
	if err != nil {
		return fmt.Errorf("skill file not found: %w", err)
	}

	skillDir := filepath.Join(projectDir, "skills")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("failed to create skills directory: %w", err)
	}

	return os.WriteFile(filepath.Join(skillDir, "towline-devops.md"), content, 0644)
}

// TemplatePack defines a template pack with compose, skills, and MCP configs.
type TemplatePack struct {
	Name        string                   `yaml:"name"`
	Description string                   `yaml:"description"`
	Compose     string                   `yaml:"compose"`
	Skills      []string                 `yaml:"skills"`
	MCPs        map[string]PackMCPConfig `yaml:"mcps"`
}

// PackMCPConfig defines an additional MCP server to include in agent configs.
type PackMCPConfig struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// loadPack tries to load a template pack by name.
// Checks ~/.towline/packs/{name}/ first, then embedded packs/{name}/.
// Returns nil if no pack exists (falls back to simple compose template).
func loadPack(name string) (*TemplatePack, string, error) {
	home, _ := os.UserHomeDir()

	// Check user packs first
	userPackDir := filepath.Join(home, ".towline", "packs", name)
	if _, err := os.Stat(filepath.Join(userPackDir, "pack.yaml")); err == nil {
		pack, err := loadPackFromDir(userPackDir)
		return pack, userPackDir, err
	}

	// Check embedded packs
	packYAML, err := towline.EmbeddedTemplates.ReadFile("templates/packs/" + name + "/pack.yaml")
	if err != nil {
		return nil, "", nil // No pack found, not an error
	}

	var pack TemplatePack
	if err := yaml.Unmarshal(packYAML, &pack); err != nil {
		return nil, "", fmt.Errorf("invalid pack.yaml for '%s': %w", name, err)
	}

	return &pack, "", nil // empty dir means embedded
}

func loadPackFromDir(dir string) (*TemplatePack, error) {
	data, err := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	if err != nil {
		return nil, err
	}

	var pack TemplatePack
	if err := yaml.Unmarshal(data, &pack); err != nil {
		return nil, fmt.Errorf("invalid pack.yaml: %w", err)
	}

	return &pack, nil
}

// applyPack copies pack skills and compose into the project, and returns
// additional MCP configs to merge into agent settings.
func applyPack(pack *TemplatePack, packDir, projectDir string) error {
	skillDir := filepath.Join(projectDir, "skills")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}

	for _, skill := range pack.Skills {
		content, err := readPackFile(pack.Name, packDir, skill)
		if err != nil {
			return fmt.Errorf("skill '%s' not found in pack '%s': %w", skill, pack.Name, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, skill), content, 0644); err != nil {
			return err
		}
	}

	return nil
}

// readPackComposeContent reads the compose file from a pack.
func readPackComposeContent(pack *TemplatePack, packDir string) (string, error) {
	content, err := readPackFile(pack.Name, packDir, pack.Compose)
	if err != nil {
		return "", fmt.Errorf("compose file '%s' not found in pack '%s': %w", pack.Compose, pack.Name, err)
	}
	return string(content), nil
}

// mergePackMCPs merges additional MCP server configurations from a template pack
// into the agent settings files (.claude/settings.json, .cursor/mcp.json, .gemini/settings.json).
func mergePackMCPs(mcps map[string]PackMCPConfig, projectDir string) {
	configFiles := []string{
		filepath.Join(projectDir, ".claude", "settings.json"),
		filepath.Join(projectDir, ".cursor", "mcp.json"),
		filepath.Join(projectDir, ".gemini", "settings.json"),
	}

	for _, cfgPath := range configFiles {
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			continue
		}

		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}

		mcpServers, ok := cfg["mcpServers"].(map[string]any)
		if !ok {
			mcpServers = make(map[string]any)
		}

		for name, mcp := range mcps {
			entry := map[string]any{
				"command": mcp.Command,
				"args":    mcp.Args,
			}
			if len(mcp.Env) > 0 {
				entry["env"] = mcp.Env
			}
			mcpServers[name] = entry
		}

		cfg["mcpServers"] = mcpServers

		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			continue
		}

		// Preserve original permissions
		os.WriteFile(cfgPath, out, 0644)
	}
}

// readPackFile reads a file from a pack directory (user or embedded).
func readPackFile(packName, packDir, filename string) ([]byte, error) {
	// If packDir is set, read from filesystem
	if packDir != "" {
		absPackDir, err := filepath.Abs(packDir)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve pack directory: %w", err)
		}
		target := filepath.Join(absPackDir, filename)
		// Prevent path traversal: target must be within packDir
		if !strings.HasPrefix(filepath.Clean(target), absPackDir+string(os.PathSeparator)) && filepath.Clean(target) != absPackDir {
			return nil, fmt.Errorf("path traversal detected in pack file: %s", filename)
		}
		return os.ReadFile(target)
	}

	// Read from embedded
	return fs.ReadFile(towline.EmbeddedTemplates, "templates/packs/"+packName+"/"+filename)
}
