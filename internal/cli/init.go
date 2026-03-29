package cli

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"

	towline "github.com/changethisusername/towline"
	"github.com/changethisusername/towline/pkg/config"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	tier := fs.String("tier", "dev", "Deployment tier (dev or prod)")
	tmpl := fs.String("template", "default", "Compose template name")
	dir := fs.String("dir", "", "Use an existing directory instead of creating a new one")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: towline init [--tier dev|prod] [--template name] [--dir path] <project-name>")
	}

	projectName := fs.Arg(0)

	if *tier != "dev" && *tier != "prod" {
		return fmt.Errorf("tier must be 'dev' or 'prod', got '%s'", *tier)
	}

	// Load global config
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	// Determine project directory and whether it's an existing codebase
	existingDir := *dir != ""
	var projectDir string

	if existingDir {
		projectDir, err = filepath.Abs(*dir)
		if err != nil {
			return fmt.Errorf("failed to resolve directory path: %w", err)
		}
		if _, err := os.Stat(projectDir); os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", projectDir)
		}
		fmt.Printf("Adding Towline to existing project '%s' in %s\n", projectName, projectDir)
	} else {
		projectDir = filepath.Join(globalCfg.ProjectsDir, projectName)
		if _, err := os.Stat(projectDir); err == nil {
			return fmt.Errorf("project directory already exists: %s (use --dir to add Towline to an existing project)", projectDir)
		}
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			return fmt.Errorf("failed to create project directory: %w", err)
		}
		fmt.Printf("Creating project '%s' in %s\n", projectName, projectDir)
	}

	// Initialize Portainer API
	api := config.NewPortainerAPI(globalCfg.PortainerURL)
	api.Token = globalCfg.PortainerAPIKey

	// Provision dev tier
	stackName := projectName + "-" + *tier
	teamID, userID, apiToken, stackID, err := provisionTier(api, globalCfg, projectName, stackName, *tmpl)
	if err != nil {
		if !existingDir {
			os.RemoveAll(projectDir)
		}
		return fmt.Errorf("failed to provision %s tier: %w", *tier, err)
	}

	// Cleanup helper for failures after provisioning
	cleanupPortainer := func() {
		fmt.Println("Cleaning up Portainer resources...")
		if stackID > 0 {
			_ = api.DeleteStack(stackID, globalCfg.PortainerEnvID)
		}
		if userID > 0 {
			_ = api.DeleteUser(userID)
		}
		if teamID > 0 {
			_ = api.DeleteTeam(teamID)
		}
		if !existingDir {
			os.RemoveAll(projectDir)
		}
	}

	// Resolve MCP binary path
	mcpBinaryPath := "towline-mcp"
	if exePath, err := os.Executable(); err == nil {
		mcpDir := filepath.Dir(exePath)
		candidate := filepath.Join(mcpDir, "towline-mcp")
		if _, err := os.Stat(candidate); err == nil {
			mcpBinaryPath = candidate
		}
	}

	// Template data for rendering
	data := TemplateData{
		ProjectName:   projectName,
		StackName:     stackName,
		Tier:          *tier,
		PortainerURL:  globalCfg.PortainerURL,
		EnvironmentID: globalCfg.PortainerEnvID,
		APIToken:      apiToken,
		MCPBinaryPath: mcpBinaryPath,
		TeamID:        teamID,
		UserID:        userID,
	}

	// Render templates
	fmt.Print("Generating project files... ")

	// Write MCP configs: .mcp.json is the primary config (Claude Code reads this)
	// Also write Cursor and Gemini configs for multi-agent support
	templates := []struct {
		name   string
		output string
	}{
		{"mcp-json.tmpl", filepath.Join(projectDir, ".mcp.json")},
		{"claude-settings.tmpl", filepath.Join(projectDir, ".claude", "settings.json")},
		{"cursor-mcp.tmpl", filepath.Join(projectDir, ".cursor", "mcp.json")},
		{"gemini-settings.tmpl", filepath.Join(projectDir, ".gemini", "settings.json")},
	}
	if !existingDir {
		templates = append(templates,
			struct{ name, output string }{"gitignore.tmpl", filepath.Join(projectDir, ".gitignore")},
		)
	}

	for _, t := range templates {
		if err := renderTemplate(t.name, data, t.output); err != nil {
			cleanupPortainer()
			return fmt.Errorf("failed to render %s: %w", t.name, err)
		}
	}

	// Write agent instructions: append to existing CLAUDE.md or create new one
	agentMDContent, err := renderTemplateToString("agent-md.tmpl", data)
	if err != nil {
		cleanupPortainer()
		return fmt.Errorf("failed to render agent instructions: %w", err)
	}

	claudeMDPath := filepath.Join(projectDir, "CLAUDE.md")
	if existingDir {
		if _, statErr := os.Stat(claudeMDPath); statErr == nil {
			// Append to existing CLAUDE.md
			f, err := os.OpenFile(claudeMDPath, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to open CLAUDE.md: %w", err)
			}
			_, err = f.WriteString("\n\n" + agentMDContent)
			f.Close()
			if err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to append to CLAUDE.md: %w", err)
			}
		} else {
			// No existing CLAUDE.md — create one
			if err := os.WriteFile(claudeMDPath, []byte(agentMDContent), 0644); err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to write CLAUDE.md: %w", err)
			}
		}
	} else {
		// New project — write CLAUDE.md (not AGENT.md)
		if err := os.WriteFile(claudeMDPath, []byte(agentMDContent), 0644); err != nil {
			cleanupPortainer()
			return fmt.Errorf("failed to write CLAUDE.md: %w", err)
		}
	}

	// For new projects, write compose files and .env.example
	// For existing dirs, only apply pack skills (skip compose and .env)
	if !existingDir {
		pack, packDir, packErr := loadPack(*tmpl)
		if packErr != nil {
			cleanupPortainer()
			return fmt.Errorf("failed to load template pack: %w", packErr)
		}

		if pack != nil {
			composeContent, err := readPackComposeContent(pack, packDir)
			if err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to read pack compose: %w", err)
			}
			if err := os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte(composeContent), 0644); err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to write compose: %w", err)
			}
			if err := applyPack(pack, packDir, projectDir); err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to apply pack: %w", err)
			}
			if len(pack.MCPs) > 0 {
				mergePackMCPs(pack.MCPs, projectDir)
			}
		} else {
			composeTemplate := "compose/" + *tmpl + ".yml"
			if err := renderTemplate(composeTemplate, data, filepath.Join(projectDir, "docker-compose.yml")); err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to render compose template: %w", err)
			}
		}

		if err := os.WriteFile(filepath.Join(projectDir, ".env.example"), []byte("# Environment variables for "+projectName+"\n"), 0644); err != nil {
			return fmt.Errorf("failed to create .env.example: %w", err)
		}
	} else {
		// Existing dir: still apply pack skills if a pack was specified
		if *tmpl != "default" {
			pack, packDir, packErr := loadPack(*tmpl)
			if packErr == nil && pack != nil {
				if err := applyPack(pack, packDir, projectDir); err != nil {
					cleanupPortainer()
					return fmt.Errorf("failed to apply pack skills: %w", err)
				}
				if len(pack.MCPs) > 0 {
					mergePackMCPs(pack.MCPs, projectDir)
				}
			}
		}
	}

	// Copy base DevOps skill (always included)
	if err := copySkillFile(projectDir); err != nil {
		cleanupPortainer()
		return fmt.Errorf("failed to copy skill file: %w", err)
	}

	fmt.Println("OK")

	// Save project config (towline.json)
	projectCfg := &config.ProjectConfig{
		StackName: stackName,
		StackID:   stackID,
		Tier:      *tier,
		TeamID:    teamID,
		UserID:    userID,
		EnvID:     globalCfg.PortainerEnvID,
		MCPBinary: mcpBinaryPath,
		MCPArgs: config.MCPArgs{
			Server: globalCfg.PortainerURL,
			Token:  apiToken,
			Stack:  stackName,
			Tier:   *tier,
		},
	}
	if err := config.SaveProjectConfig(projectDir, projectCfg); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}

	// Git init (skip for existing directories — they already have a repo)
	if !existingDir {
		fmt.Print("Initializing git repository... ")
		if err := gitInit(projectDir); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println()
	fmt.Printf("Project '%s' created successfully!\n", projectName)
	fmt.Printf("  Directory: %s\n", projectDir)
	fmt.Printf("  Stack:     %s\n", stackName)
	fmt.Printf("  Tier:      %s\n", *tier)
	fmt.Println()
	fmt.Println("Open the project in your editor to start using the MCP tools.")

	return nil
}

// provisionTier creates Portainer resources for a project tier.
// On partial failure, it cleans up any resources it already created.
func provisionTier(api *config.PortainerAPI, globalCfg *config.GlobalConfig, projectName, stackName, tmpl string) (teamID, userID int, apiToken string, stackID int, err error) {
	// Cleanup on failure: delete any resources we created if an error occurs
	defer func() {
		if err == nil {
			return
		}
		if userID > 0 {
			fmt.Printf("Cleaning up user (ID: %d)... ", userID)
			if delErr := api.DeleteUser(userID); delErr != nil {
				fmt.Printf("warning: %v\n", delErr)
			} else {
				fmt.Println("OK")
			}
		}
		if teamID > 0 {
			fmt.Printf("Cleaning up team (ID: %d)... ", teamID)
			if delErr := api.DeleteTeam(teamID); delErr != nil {
				fmt.Printf("warning: %v\n", delErr)
			} else {
				fmt.Println("OK")
			}
		}
	}()

	// Create team
	teamName := "team-" + stackName
	fmt.Printf("Creating team '%s'... ", teamName)
	teamID, err = api.CreateTeam(teamName)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to create team: %w", err)
	}
	fmt.Println("OK")

	// Grant team access to environment
	fmt.Print("Granting environment access... ")
	if err = api.SetEndpointTeamAccess(globalCfg.PortainerEnvID, teamID); err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to set endpoint access: %w", err)
	}
	fmt.Println("OK")

	// Create user for the project
	username := "towline-" + stackName
	password, err := generatePassword(24)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to generate password: %w", err)
	}

	fmt.Printf("Creating user '%s'... ", username)
	userID, err = api.CreateUser(username, password, 2) // role 2 = standard user
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to create user: %w", err)
	}
	fmt.Println("OK")

	// Add user to team
	fmt.Print("Adding user to team... ")
	if err = api.AddTeamMember(teamID, userID); err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to add team member: %w", err)
	}
	fmt.Println("OK")

	// Generate API token for the user.
	// Portainer requires authenticating AS the user to generate their token.
	fmt.Print("Generating project API token... ")
	savedToken := api.Token
	userJWT, err := api.Authenticate(username, password)
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to authenticate as project user: %w", err)
	}
	api.Token = userJWT
	apiToken, err = api.GenerateAPIToken(userID, "towline-"+stackName, password)
	if err != nil {
		api.Token = savedToken
		return 0, 0, "", 0, fmt.Errorf("failed to generate API token: %w", err)
	}
	fmt.Println("OK")

	// Read compose template — fail fast if template doesn't exist
	composeContent := "services: {}\n"
	composePath := "compose/" + tmpl + ".yml"
	if content, readErr := readTemplateContent(composePath); readErr == nil {
		composeContent = string(content)
	} else if tmpl != "default" {
		api.Token = savedToken
		return 0, 0, "", 0, fmt.Errorf("template '%s' not found: %w", tmpl, readErr)
	}

	// Create stack as the project user so they own it.
	// Stack ownership in Portainer is tied to the creating user — if we create
	// as admin, the project user gets 403 on update/stop/delete.
	fmt.Printf("Creating stack '%s'... ", stackName)
	stackID, err = api.CreateLocalStack(globalCfg.PortainerEnvID, stackName, composeContent)
	api.Token = savedToken // restore admin token after stack creation
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to create stack: %w", err)
	}
	fmt.Println("OK")

	return teamID, userID, apiToken, stackID, nil
}

// readTemplateContent reads a template file, checking user overrides first.
func readTemplateContent(name string) ([]byte, error) {
	home, _ := os.UserHomeDir()
	userPath := filepath.Join(home, ".towline", "templates", name)

	if _, err := os.Stat(userPath); err == nil {
		return os.ReadFile(userPath)
	}

	return readEmbeddedTemplate(name)
}

// readEmbeddedTemplate reads a template from the embedded filesystem.
func readEmbeddedTemplate(name string) ([]byte, error) {
	return towline.EmbeddedTemplates.ReadFile("templates/" + name)
}

// generatePassword generates a random password of the given length.
func generatePassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[n.Int64()]
	}
	return string(result), nil
}

// gitInit initializes a git repository and creates an initial commit.
func gitInit(projectDir string) error {
	cmds := []struct {
		name string
		args []string
	}{
		{"git", []string{"init"}},
		{"git", []string{"add", "-A"}},
		{"git", []string{"commit", "-m", "Initial commit (scaffolded by towline)"}},
	}

	for _, c := range cmds {
		cmd := exec.Command(c.name, c.args...)
		cmd.Dir = projectDir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("'%s %v' failed: %w", c.name, c.args, err)
		}
	}

	return nil
}
