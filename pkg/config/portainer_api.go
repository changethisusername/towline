package config

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StandardUserRoleName is the Portainer environment role project teams get:
// it can manage the stacks the team owns, but not other teams' resources.
const StandardUserRoleName = "Standard user"

// fallbackStandardUserRoleID is the Standard user role's ID, used only when
// Portainer doesn't list its roles. Portainer's built-in roles are
// 1 Environment administrator, 2 Helpdesk, 3 Standard user, 4 Read-only user
// and 5 Operator.
const fallbackStandardUserRoleID = 3

// stackUpdateAuthorization is the role authorization Portainer checks before
// updating a stack.
const stackUpdateAuthorization = "PortainerStackUpdate"

// requestTimeout bounds each Portainer API call so an unresponsive server
// can't hang the CLI. Creating or deleting a stack runs docker compose (and
// image pulls) inside the request, so those calls get stackRequestTimeout.
const (
	requestTimeout      = 60 * time.Second
	stackRequestTimeout = 10 * time.Minute
)

// PortainerAPI is a lightweight HTTP client for Portainer REST API endpoints
// not available in the upstream SDK. Used by the CLI for setup and provisioning.
type PortainerAPI struct {
	BaseURL string
	Token   string
	client  *http.Client
	// stackClient is client with the longer stackRequestTimeout.
	stackClient *http.Client
}

// NewPortainerAPI creates a new Portainer API client. TLS certificates are
// verified unless skipTLSVerify is set (for self-signed certificates); that
// option exposes the admin credentials to anyone who can intercept traffic.
func NewPortainerAPI(baseURL string, skipTLSVerify bool) *PortainerAPI {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &PortainerAPI{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		client:      &http.Client{Transport: transport, Timeout: requestTimeout},
		stackClient: &http.Client{Transport: transport, Timeout: stackRequestTimeout},
	}
}

// doRequest executes an HTTP request against the Portainer API.
func (p *PortainerAPI) doRequest(method, path string, body []byte) (*http.Response, error) {
	return p.doRequestWith(p.client, method, path, body)
}

// doRequestWith executes an HTTP request using the given client.
func (p *PortainerAPI) doRequestWith(client *http.Client, method, path string, body []byte) (*http.Response, error) {
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

	resp, err := client.Do(req)
	if err != nil {
		var certErr *tls.CertificateVerificationError
		if errors.As(err, &certErr) {
			return nil, fmt.Errorf("failed to verify Portainer's TLS certificate (for a self-signed certificate, re-run 'towline setup' and choose to skip verification): %w", err)
		}
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

// Role is a Portainer environment role.
type Role struct {
	ID             int             `json:"Id"`
	Name           string          `json:"Name"`
	Authorizations map[string]bool `json:"Authorizations"`
}

// CanUpdateStacks reports whether the role may update stacks. known is
// false when Portainer listed no authorizations for the role.
func (r Role) CanUpdateStacks() (can, known bool) {
	if len(r.Authorizations) == 0 {
		return false, false
	}
	return r.Authorizations[stackUpdateAuthorization], true
}

// errRolesUnavailable means this Portainer doesn't serve /api/roles.
var errRolesUnavailable = errors.New("portainer does not list roles")

// ListRoles returns Portainer's environment roles.
func (p *PortainerAPI) ListRoles() ([]Role, error) {
	resp, err := p.doRequest("GET", "/api/roles", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list roles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errRolesUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list roles (status %d): %s", resp.StatusCode, string(body))
	}

	var roles []Role
	if err := json.NewDecoder(resp.Body).Decode(&roles); err != nil {
		return nil, fmt.Errorf("failed to decode roles: %w", err)
	}
	return roles, nil
}

// listRolesOrFallback lists Portainer's roles. On a Portainer that doesn't
// list them it returns only the built-in Standard user role.
func (p *PortainerAPI) listRolesOrFallback() ([]Role, error) {
	roles, err := p.ListRoles()
	if errors.Is(err, errRolesUnavailable) {
		return []Role{{ID: fallbackStandardUserRoleID, Name: StandardUserRoleName}}, nil
	}
	return roles, err
}

// findRole returns the role with the given ID, or a placeholder naming the ID.
func findRole(roles []Role, id int) Role {
	for _, r := range roles {
		if r.ID == id {
			return r
		}
	}
	if id == 0 {
		return Role{Name: "no access"}
	}
	return Role{ID: id, Name: fmt.Sprintf("unknown role %d", id)}
}

// standardUserRole finds the Standard user role by name, so the ID never
// depends on how a Portainer edition numbers its roles.
func standardUserRole(roles []Role) (Role, error) {
	for _, r := range roles {
		if strings.EqualFold(r.Name, StandardUserRoleName) {
			return r, nil
		}
	}
	return Role{}, fmt.Errorf("portainer has no %q role", StandardUserRoleName)
}

// SetEndpointTeamAccess gives a team the Standard user role on a Portainer
// environment (endpoint) and returns the role Portainer reports afterwards.
// It fails if Portainer reports any other role.
func (p *PortainerAPI) SetEndpointTeamAccess(endpointID, teamID int) (Role, error) {
	roles, err := p.listRolesOrFallback()
	if err != nil {
		return Role{}, err
	}
	want, err := standardUserRole(roles)
	if err != nil {
		return Role{}, err
	}

	endpoint, err := p.getEndpoint(endpointID)
	if err != nil {
		return Role{}, err
	}

	// Update team access policies
	teamAccessPolicies := map[string]any{}
	if existing, ok := endpoint["TeamAccessPolicies"].(map[string]any); ok {
		teamAccessPolicies = existing
	}
	teamAccessPolicies[fmt.Sprintf("%d", teamID)] = map[string]any{
		// Standard user: can manage stacks it owns, but not other teams'
		// resources. Environment administrator would let every project's
		// key manage every other project's stacks on the shared environment,
		// and Read-only user can't update stacks at all.
		"RoleId": want.ID,
	}

	payload, err := json.Marshal(map[string]any{
		"TeamAccessPolicies": teamAccessPolicies,
	})
	if err != nil {
		return Role{}, fmt.Errorf("failed to marshal endpoint update: %w", err)
	}

	resp, err := p.doRequest("PUT", fmt.Sprintf("/api/endpoints/%d", endpointID), payload)
	if err != nil {
		return Role{}, fmt.Errorf("failed to update endpoint access: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Role{}, fmt.Errorf("failed to update endpoint access (status %d): %s", resp.StatusCode, string(body))
	}

	// Read the role back rather than trusting the update.
	got, err := p.teamRole(endpointID, teamID, roles)
	if err != nil {
		return Role{}, err
	}
	if got.ID != want.ID {
		return got, fmt.Errorf("portainer reports the team's role as %q, not %q", got.Name, want.Name)
	}
	return got, nil
}

// TeamRole returns the role a team holds on a Portainer environment.
func (p *PortainerAPI) TeamRole(endpointID, teamID int) (Role, error) {
	roles, err := p.listRolesOrFallback()
	if err != nil {
		return Role{}, err
	}
	return p.teamRole(endpointID, teamID, roles)
}

func (p *PortainerAPI) teamRole(endpointID, teamID int, roles []Role) (Role, error) {
	endpoint, err := p.getEndpoint(endpointID)
	if err != nil {
		return Role{}, err
	}
	policies, _ := endpoint["TeamAccessPolicies"].(map[string]any)
	policy, _ := policies[fmt.Sprintf("%d", teamID)].(map[string]any)
	id, _ := policy["RoleId"].(float64)
	return findRole(roles, int(id)), nil
}

// getEndpoint returns a Portainer environment's raw configuration.
func (p *PortainerAPI) getEndpoint(endpointID int) (map[string]any, error) {
	resp, err := p.doRequest("GET", fmt.Sprintf("/api/endpoints/%d", endpointID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get endpoint (status %d): %s", resp.StatusCode, string(body))
	}

	var endpoint map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&endpoint); err != nil {
		return nil, fmt.Errorf("failed to decode endpoint: %w", err)
	}
	return endpoint, nil
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

	resp, err := p.doRequestWith(p.stackClient, "POST",
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
	resp, err := p.doRequestWith(p.stackClient, "DELETE",
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

// StackInfo is the subset of a Portainer stack used to verify ownership.
type StackInfo struct {
	ID         int    `json:"Id"`
	Name       string `json:"Name"`
	EndpointID int    `json:"EndpointId"`
}

// GetStack returns a stack by ID.
func (p *PortainerAPI) GetStack(stackID int) (*StackInfo, error) {
	var out StackInfo
	if err := p.getJSON(fmt.Sprintf("/api/stacks/%d", stackID), &out); err != nil {
		return nil, fmt.Errorf("failed to get stack: %w", err)
	}
	return &out, nil
}

// GetTeamName returns the name of a team by ID.
func (p *PortainerAPI) GetTeamName(teamID int) (string, error) {
	var out struct {
		Name string `json:"Name"`
	}
	if err := p.getJSON(fmt.Sprintf("/api/teams/%d", teamID), &out); err != nil {
		return "", fmt.Errorf("failed to get team: %w", err)
	}
	return out.Name, nil
}

// GetUsername returns the username of a user by ID.
func (p *PortainerAPI) GetUsername(userID int) (string, error) {
	var out struct {
		Username string `json:"Username"`
	}
	if err := p.getJSON(fmt.Sprintf("/api/users/%d", userID), &out); err != nil {
		return "", fmt.Errorf("failed to get user: %w", err)
	}
	return out.Username, nil
}

func (p *PortainerAPI) getJSON(path string, out any) error {
	resp, err := p.doRequest("GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GetStackFile returns a stack's compose file content.
func (p *PortainerAPI) GetStackFile(stackID int) (string, error) {
	var out struct {
		StackFileContent string `json:"StackFileContent"`
	}
	if err := p.getJSON(fmt.Sprintf("/api/stacks/%d/file", stackID), &out); err != nil {
		return "", fmt.Errorf("failed to get stack file: %w", err)
	}
	return out.StackFileContent, nil
}
