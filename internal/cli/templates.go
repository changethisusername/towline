package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	towline "github.com/changethisusername/towline"
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
