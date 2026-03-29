package cli

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/changethisusername/towline/pkg/config"
)

func runSetup(args []string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("=== Towline Setup ===")
	fmt.Println()

	// Prompt for Portainer URL
	fmt.Print("Portainer URL (e.g., https://192.168.1.50:9443): ")
	portainerURL, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	portainerURL = strings.TrimSpace(portainerURL)
	if portainerURL == "" {
		return fmt.Errorf("Portainer URL is required")
	}

	// Prompt for username
	fmt.Print("Portainer admin username: ")
	username, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is required")
	}

	// Prompt for password
	fmt.Print("Portainer admin password: ")
	password, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return fmt.Errorf("password is required")
	}

	// Authenticate
	fmt.Println()
	fmt.Print("Authenticating... ")
	api := config.NewPortainerAPI(portainerURL)
	jwt, err := api.Authenticate(username, password)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}
	fmt.Println("OK")

	// Use JWT temporarily for subsequent calls
	api.Token = jwt

	// Decode user ID from JWT
	userID, err := decodeUserIDFromJWT(jwt)
	if err != nil {
		return fmt.Errorf("failed to decode user ID from JWT: %w", err)
	}

	// Generate API token
	fmt.Print("Generating API token... ")
	apiToken, err := api.GenerateAPIToken(userID, "towline-admin", password)
	if err != nil {
		return fmt.Errorf("failed to generate API token: %w", err)
	}
	fmt.Println("OK")

	// Switch to using the API token
	api.Token = apiToken

	// List environments
	fmt.Println()
	fmt.Println("Available environments:")
	envs, err := api.ListEnvironments()
	if err != nil {
		return fmt.Errorf("failed to list environments: %w", err)
	}

	if len(envs) == 0 {
		return fmt.Errorf("no environments found in Portainer")
	}

	for i, env := range envs {
		name, _ := env["Name"].(string)
		idFloat, _ := env["Id"].(float64)
		envType, _ := env["Type"].(float64)
		typeStr := "unknown"
		switch int(envType) {
		case 1:
			typeStr = "Docker"
		case 2:
			typeStr = "Agent"
		case 3:
			typeStr = "Azure"
		case 4:
			typeStr = "Edge Agent"
		case 5:
			typeStr = "Kubernetes"
		}
		fmt.Printf("  [%d] %s (ID: %d, Type: %s)\n", i+1, name, int(idFloat), typeStr)
	}

	fmt.Print("\nSelect environment (number): ")
	envInput, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	envInput = strings.TrimSpace(envInput)
	envIndex, err := strconv.Atoi(envInput)
	if err != nil || envIndex < 1 || envIndex > len(envs) {
		return fmt.Errorf("invalid selection: %s", envInput)
	}

	selectedEnv := envs[envIndex-1]
	envID := int(selectedEnv["Id"].(float64))

	// Prompt for projects directory
	home, _ := os.UserHomeDir()
	defaultProjectsDir := home + "/projects"
	fmt.Printf("Projects directory [%s]: ", defaultProjectsDir)
	projectsDir, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	projectsDir = strings.TrimSpace(projectsDir)
	if projectsDir == "" {
		projectsDir = defaultProjectsDir
	}

	// Expand ~ in path
	if strings.HasPrefix(projectsDir, "~/") {
		projectsDir = home + projectsDir[1:]
	}

	// Save configuration
	cfg := &config.GlobalConfig{
		PortainerURL:    portainerURL,
		PortainerAPIKey: apiToken,
		PortainerEnvID:  envID,
		ProjectsDir:     projectsDir,
	}

	if err := config.SaveGlobalConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Create projects directory
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		return fmt.Errorf("failed to create projects directory: %w", err)
	}

	fmt.Println()
	fmt.Println("Setup complete!")
	cfgPath, _ := config.GlobalConfigPath()
	fmt.Printf("Config saved to %s\n", cfgPath)
	fmt.Printf("Projects directory: %s\n", projectsDir)
	fmt.Println()
	fmt.Println("Next step: run 'towline init <project-name>' to create a project.")

	return nil
}

// decodeUserIDFromJWT extracts the user ID from a Portainer JWT token.
// The JWT payload (2nd base64 segment) contains an "id" field with the user ID.
func decodeUserIDFromJWT(jwt string) (int, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid JWT format")
	}

	// Decode the payload (2nd segment)
	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard encoding
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return 0, fmt.Errorf("failed to decode JWT payload: %w", err)
		}
	}

	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return 0, fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	idVal, ok := claims["id"]
	if !ok {
		return 0, fmt.Errorf("no 'id' field in JWT claims")
	}

	switch v := idVal.(type) {
	case float64:
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("failed to parse user ID: %w", err)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("unexpected type for 'id' field: %T", idVal)
	}
}
