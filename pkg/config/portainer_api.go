package config

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// PortainerAPI is a lightweight HTTP client for Portainer REST API endpoints
// not available in the upstream SDK. Used by the CLI for setup and provisioning.
type PortainerAPI struct {
	BaseURL string
	Token   string
	client  *http.Client
}

// NewPortainerAPI creates a new Portainer API client.
func NewPortainerAPI(baseURL string) *PortainerAPI {
	return &PortainerAPI{
		BaseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// doRequest executes an HTTP request against the Portainer API.
func (p *PortainerAPI) doRequest(method, path string, body []byte) (*http.Response, error) {
	url := p.BaseURL + path

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.Token != "" {
		// Portainer uses X-API-Key for long-lived API tokens and
		// Authorization: Bearer for JWTs. JWTs have three dot-separated segments.
		if strings.Count(p.Token, ".") == 2 {
			req.Header.Set("Authorization", "Bearer "+p.Token)
		} else {
			req.Header.Set("X-API-Key", p.Token)
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	return resp, nil
}

// Authenticate authenticates with Portainer and returns a JWT token.
func (p *PortainerAPI) Authenticate(username, password string) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal auth payload: %w", err)
	}

	resp, err := p.doRequest("POST", "/api/auth", payload)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("authentication failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		JWT string `json:"jwt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode auth response: %w", err)
	}

	return result.JWT, nil
}

// GenerateAPIToken generates a long-lived API token for a user.
// The Portainer API requires the user's password to confirm token creation.
func (p *PortainerAPI) GenerateAPIToken(userID int, description, password string) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"description": description,
		"password":    password,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal token payload: %w", err)
	}

	resp, err := p.doRequest("POST", fmt.Sprintf("/api/users/%d/tokens", userID), payload)
	if err != nil {
		return "", fmt.Errorf("failed to generate API token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to generate API token (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		RawAPIKey string `json:"rawAPIKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return result.RawAPIKey, nil
}

// CreateTeam creates a new Portainer team and returns its ID.
func (p *PortainerAPI) CreateTeam(name string) (int, error) {
	payload, err := json.Marshal(map[string]string{
		"name": name,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal team payload: %w", err)
	}

	resp, err := p.doRequest("POST", "/api/teams", payload)
	if err != nil {
		return 0, fmt.Errorf("failed to create team: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to create team (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID int `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode team response: %w", err)
	}

	return result.ID, nil
}

// CreateUser creates a new Portainer user and returns their ID.
// role: 1 = admin, 2 = standard user
func (p *PortainerAPI) CreateUser(username, password string, role int) (int, error) {
	payload, err := json.Marshal(map[string]any{
		"username": username,
		"password": password,
		"role":     role,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal user payload: %w", err)
	}

	resp, err := p.doRequest("POST", "/api/users", payload)
	if err != nil {
		return 0, fmt.Errorf("failed to create user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to create user (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID int `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode user response: %w", err)
	}

	return result.ID, nil
}

// AddTeamMember adds a user to a team.
func (p *PortainerAPI) AddTeamMember(teamID, userID int) error {
	payload, err := json.Marshal(map[string]any{
		"TeamID": teamID,
		"UserID": userID,
		"Role":   2, // team member
	})
	if err != nil {
		return fmt.Errorf("failed to marshal membership payload: %w", err)
	}

	resp, err := p.doRequest("POST", "/api/team_memberships", payload)
	if err != nil {
		return fmt.Errorf("failed to add team member: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add team member (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetEndpointTeamAccess grants a team access to a Portainer environment (endpoint).
func (p *PortainerAPI) SetEndpointTeamAccess(endpointID, teamID int) error {
	// First, get current endpoint configuration
	resp, err := p.doRequest("GET", fmt.Sprintf("/api/endpoints/%d", endpointID), nil)
	if err != nil {
		return fmt.Errorf("failed to get endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to get endpoint (status %d): %s", resp.StatusCode, string(body))
	}

	var endpoint map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&endpoint); err != nil {
		return fmt.Errorf("failed to decode endpoint: %w", err)
	}

	// Update team access policies
	teamAccessPolicies := map[string]any{}
	if existing, ok := endpoint["TeamAccessPolicies"].(map[string]any); ok {
		teamAccessPolicies = existing
	}
	teamAccessPolicies[fmt.Sprintf("%d", teamID)] = map[string]any{
		"RoleId": 1, // environment administrator — needed for stack CRUD
	}

	payload, err := json.Marshal(map[string]any{
		"TeamAccessPolicies": teamAccessPolicies,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal endpoint update: %w", err)
	}

	resp2, err := p.doRequest("PUT", fmt.Sprintf("/api/endpoints/%d", endpointID), payload)
	if err != nil {
		return fmt.Errorf("failed to update endpoint access: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("failed to update endpoint access (status %d): %s", resp2.StatusCode, string(body))
	}

	return nil
}

// ListEnvironments returns all Portainer environments (endpoints).
func (p *PortainerAPI) ListEnvironments() ([]map[string]any, error) {
	resp, err := p.doRequest("GET", "/api/endpoints", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list environments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list environments (status %d): %s", resp.StatusCode, string(body))
	}

	var result []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode environments: %w", err)
	}

	return result, nil
}

// CreateLocalStack creates a new standalone Docker Compose stack.
func (p *PortainerAPI) CreateLocalStack(endpointID int, name, composeContent string) (int, error) {
	payload, err := json.Marshal(map[string]any{
		"name":             name,
		"stackFileContent": composeContent,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal stack payload: %w", err)
	}

	resp, err := p.doRequest("POST",
		fmt.Sprintf("/api/stacks/create/standalone/string?endpointId=%d", endpointID),
		payload)
	if err != nil {
		return 0, fmt.Errorf("failed to create stack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to create stack (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID int `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode stack response: %w", err)
	}

	return result.ID, nil
}

// DeleteTeam deletes a Portainer team by ID.
func (p *PortainerAPI) DeleteTeam(teamID int) error {
	resp, err := p.doRequest("DELETE", fmt.Sprintf("/api/teams/%d", teamID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete team: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete team (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteUser deletes a Portainer user by ID.
func (p *PortainerAPI) DeleteUser(userID int) error {
	resp, err := p.doRequest("DELETE", fmt.Sprintf("/api/users/%d", userID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete user (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteStack deletes a Portainer stack by ID.
func (p *PortainerAPI) DeleteStack(stackID, endpointID int) error {
	resp, err := p.doRequest("DELETE",
		fmt.Sprintf("/api/stacks/%d?endpointId=%d", stackID, endpointID),
		nil)
	if err != nil {
		return fmt.Errorf("failed to delete stack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete stack (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
