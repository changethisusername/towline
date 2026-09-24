package cli

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	towline "github.com/changethisusername/towline"
	"github.com/changethisusername/towline/pkg/config"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	tier := fs.String("tier", "dev", "Deployment tier (dev or prod)")
	tmpl := fs.String("template", "default", "Compose template name")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: towline init [--tier dev|prod] [--template name] <project-name>")
	}

	projectName := fs.Arg(0)
	if err := validateProjectName(projectName); err != nil {
		return err
	}

	if *tier != "dev" && *tier != "prod" {
		return fmt.Errorf("tier must be 'dev' or 'prod', got '%s'", *tier)
	}

	// Load global config
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	// Resolve the compose file before touching Portainer, so an unknown
	// template fails fast without creating anything.
	composeContent, pack, packDir, err := resolveCompose(*tmpl, projectName)
	if err != nil {
		return err
	}

	// Detect if we're in an existing codebase (has .git, package.json, go.mod, etc.)
	cwd, _ := os.Getwd()
	existingDir := isExistingCodebase(cwd)
	var projectDir string

	if existingDir {
		projectDir = cwd
		fmt.Printf("Adding Towline to existing project '%s' in %s\n", projectName, projectDir)
	} else {
		projectDir = filepath.Join(globalCfg.ProjectsDir, projectName)
		if _, err := os.Stat(projectDir); err == nil {
			return fmt.Errorf("project directory already exists: %s", projectDir)
		}
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			return fmt.Errorf("failed to create project directory: %w", err)
		}
		fmt.Printf("Creating project '%s' in %s\n", projectName, projectDir)
	}

	// Initialize Portainer API
	api := config.NewPortainerAPI(globalCfg.PortainerURL, globalCfg.SkipTLSVerify)
	api.Token = globalCfg.PortainerAPIKey

	// Provision dev tier
	stackName := projectName + "-" + *tier
	teamID, userID, apiToken, stackID, err := provisionTier(api, globalCfg, stackName, composeContent)
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
		ProjectName:     projectName,
		StackName:       stackName,
		Tier:            *tier,
		PortainerURL:    globalCfg.PortainerURL,
		EnvironmentID:   globalCfg.PortainerEnvID,
		APIToken:        apiToken,
		MCPBinaryPath:   mcpBinaryPath,
		TeamID:          teamID,
		UserID:          userID,
		SkipTLSVerify:   globalCfg.SkipTLSVerify,
		ApprovalWebhook: globalCfg.ApprovalWebhook,
	}

	// Render templates
	fmt.Print("Generating project files... ")

	// Write MCP configs: .mcp.json is the primary config (Claude Code reads this)
	// Also write Cursor and Gemini configs for multi-agent support
	type projectTemplate struct {
		name   string
		output string
		perm   os.FileMode
	}
	templates := []projectTemplate{
		{"mcp-json.tmpl", filepath.Join(projectDir, ".mcp.json"), secretFileMode},
		{"claude-settings.tmpl", filepath.Join(projectDir, ".claude", "settings.json"), secretFileMode},
		{"cursor-mcp.tmpl", filepath.Join(projectDir, ".cursor", "mcp.json"), secretFileMode},
		{"gemini-settings.tmpl", filepath.Join(projectDir, ".gemini", "settings.json"), secretFileMode},
	}

	// Keep token-bearing files out of git before any of them is written.
	// Existing codebases keep their .gitignore, with our entries appended.
	if existingDir {
		if err := ensureGitignore(projectDir); err != nil {
			cleanupPortainer()
			return fmt.Errorf("failed to update .gitignore: %w", err)
		}
	} else {
		templates = append([]projectTemplate{{"gitignore.tmpl", filepath.Join(projectDir, ".gitignore"), publicFileMode}}, templates...)
	}

	for _, t := range templates {
		if err := renderTemplate(t.name, data, t.output, t.perm); err != nil {
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
		if err := os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte(composeContent), publicFileMode); err != nil {
			cleanupPortainer()
			return fmt.Errorf("failed to write compose: %w", err)
		}
		if pack != nil {
			if err := applyPack(pack, packDir, projectDir); err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to apply pack: %w", err)
			}
			if len(pack.MCPs) > 0 {
				mergePackMCPs(pack.MCPs, projectDir)
			}
		}

		if err := os.WriteFile(filepath.Join(projectDir, ".env.example"), []byte("# Environment variables for "+projectName+"\n"), 0644); err != nil {
			return fmt.Errorf("failed to create .env.example: %w", err)
		}
	} else {
		// Existing dir: still apply pack skills if a pack was specified
		if pack != nil {
			if err := applyPack(pack, packDir, projectDir); err != nil {
				cleanupPortainer()
				return fmt.Errorf("failed to apply pack skills: %w", err)
			}
			if len(pack.MCPs) > 0 {
				mergePackMCPs(pack.MCPs, projectDir)
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
	if *tier == "prod" && globalCfg.ApprovalWebhook == "" {
		fmt.Println()
		fmt.Println("Warning: no approval_webhook is set in ~/.towline/config.yaml. Prod operations")
		fmt.Println("that need human approval (deploys, config changes, exec) will be refused.")
	}

	return nil
}

// provisionTier creates Portainer resources for a project tier.
// On partial failure, it cleans up any resources it already created.
func provisionTier(api *config.PortainerAPI, globalCfg *config.GlobalConfig, stackName, composeContent string) (teamID, userID int, apiToken string, stackID int, err error) {
	adminToken := api.Token

	// Cleanup on failure: delete any resources we created if an error occurs.
	// Named results are left intact on error paths so this sees what exists.
	defer func() {
		// Always leave the client authenticated as admin, which cleanup needs.
		api.Token = adminToken
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
		teamID, userID, apiToken, stackID = 0, 0, "", 0
	}()

	// Create team
	teamName := "team-" + stackName
	fmt.Printf("Creating team '%s'... ", teamName)
	teamID, err = api.CreateTeam(teamName)
	if err != nil {
		err = fmt.Errorf("failed to create team: %w", err)
		return
	}
	fmt.Println("OK")

	// Grant team access to environment
	fmt.Print("Granting environment access... ")
	if err = api.SetEndpointTeamAccess(globalCfg.PortainerEnvID, teamID); err != nil {
		err = fmt.Errorf("failed to set endpoint access: %w", err)
		return
	}
	fmt.Println("OK")

	// Create user for the project
	username := "towline-" + stackName
	password, err := generatePassword(24)
	if err != nil {
		err = fmt.Errorf("failed to generate password: %w", err)
		return
	}

	fmt.Printf("Creating user '%s'... ", username)
	userID, err = api.CreateUser(username, password, 2) // role 2 = standard user
	if err != nil {
		err = fmt.Errorf("failed to create user: %w", err)
		return
	}
	fmt.Println("OK")

	// Add user to team
	fmt.Print("Adding user to team... ")
	if err = api.AddTeamMember(teamID, userID); err != nil {
		err = fmt.Errorf("failed to add team member: %w", err)
		return
	}
	fmt.Println("OK")

	// Generate API token for the user.
	// Portainer requires authenticating AS the user to generate their token.
	fmt.Print("Generating project API token... ")
	userJWT, err := api.Authenticate(username, password)
	if err != nil {
		err = fmt.Errorf("failed to authenticate as project user: %w", err)
		return
	}
	api.Token = userJWT
	apiToken, err = api.GenerateAPIToken(userID, "towline-"+stackName, password)
	if err != nil {
		err = fmt.Errorf("failed to generate API token: %w", err)
		return
	}
	fmt.Println("OK")

	// Create stack as the project user so they own it.
	// Stack ownership in Portainer is tied to the creating user — if we create
	// as admin, the project user gets 403 on update/stop/delete.
	fmt.Printf("Creating stack '%s'... ", stackName)
	stackID, err = api.CreateLocalStack(globalCfg.PortainerEnvID, stackName, composeContent)
	if err != nil {
		err = fmt.Errorf("failed to create stack: %w", err)
		return
	}
	fmt.Println("OK")

	return
}

// resolveCompose returns the compose content for a template name. A template
// pack of that name takes precedence over a plain compose template.
func resolveCompose(tmpl, projectName string) (content string, pack *TemplatePack, packDir string, err error) {
	pack, packDir, err = loadPack(tmpl)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to load template pack: %w", err)
	}
	if pack != nil {
		content, err = readPackComposeContent(pack, packDir)
		if err != nil {
			return "", nil, "", err
		}
		return content, pack, packDir, nil
	}

	content, err = renderTemplateToString("compose/"+tmpl+".yml", TemplateData{ProjectName: projectName})
	if err != nil {
		if tmpl == "default" {
			return "services: {}\n", nil, "", nil
		}
		return "", nil, "", fmt.Errorf("template '%s' not found: %w", tmpl, err)
	}
	return content, nil, "", nil
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

// isExistingCodebase checks if a directory looks like an existing project
// by looking for common project markers.
func isExistingCodebase(dir string) bool {
	markers := []string{
		".git",
		"package.json",
		"go.mod",
		"Cargo.toml",
		"pyproject.toml",
		"requirements.txt",
		"Makefile",
		"pom.xml",
		"build.gradle",
		"Gemfile",
		"composer.json",
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
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

// projectNameRegex matches valid project names. Names become part of Docker
// Compose project names, Portainer team and user names, and directory names,
// so they are restricted to lowercase letters, digits, '-' and '_'.
var projectNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func validateProjectName(name string) error {
	if !projectNameRegex.MatchString(name) {
		return fmt.Errorf("invalid project name %q: use lowercase letters, digits, '-' and '_' (starting with a letter or digit)", name)
	}
	return nil
}

// secretGitignoreEntries are the generated files that contain the project's
// Portainer API token or provisioning details.
var secretGitignoreEntries = []string{".mcp.json", ".claude/", ".cursor/", ".gemini/", "towline.json"}

// ensureGitignore appends any missing secret entries to an existing
// project's .gitignore (creating it if needed).
func ensureGitignore(projectDir string) error {
	path := filepath.Join(projectDir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	present := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimSpace(line)] = true
	}

	var missing []string
	for _, entry := range secretGitignoreEntries {
		if !present[entry] && !present["/"+entry] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# Towline agent configuration (contains API tokens)\n")
	for _, entry := range missing {
		b.WriteString(entry + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), publicFileMode)
}
