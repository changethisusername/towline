package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// StackEnvVar is a stack environment variable.
type StackEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FindStackByName returns the stack with the given name on an environment,
// or nil if there is none.
func (p *PortainerAPI) FindStackByName(endpointID int, name string) (*StackInfo, error) {
	var stacks []StackInfo
	if err := p.getJSON("/api/stacks", &stacks); err != nil {
		return nil, fmt.Errorf("failed to list stacks: %w", err)
	}
	for i := range stacks {
		if stacks[i].Name == name && stacks[i].EndpointID == endpointID {
			return &stacks[i], nil
		}
	}
	return nil, nil
}

// CreateLocalStackWithEnv creates a standalone compose stack with
// environment variables.
func (p *PortainerAPI) CreateLocalStackWithEnv(endpointID int, name, composeContent string, env []StackEnvVar) (int, error) {
	payload, err := json.Marshal(map[string]any{
		"name":             name,
		"stackFileContent": composeContent,
		"env":              env,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal stack payload: %w", err)
	}
	resp, err := p.doRequestWith(p.stackClient, "POST",
		fmt.Sprintf("/api/stacks/create/standalone/string?endpointId=%d", endpointID), payload)
	if err != nil {
		return 0, fmt.Errorf("failed to create stack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
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

// UpdateLocalStackWithEnv replaces a stack's compose file and environment
// and redeploys it, pulling images again.
func (p *PortainerAPI) UpdateLocalStackWithEnv(stackID, endpointID int, composeContent string, env []StackEnvVar) error {
	payload, err := json.Marshal(map[string]any{
		"stackFileContent": composeContent,
		"env":              env,
		"prune":            true,
		"pullImage":        true,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal stack payload: %w", err)
	}
	resp, err := p.doRequestWith(p.stackClient, "PUT",
		fmt.Sprintf("/api/stacks/%d?endpointId=%d", stackID, endpointID), payload)
	if err != nil {
		return fmt.Errorf("failed to update stack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("failed to update stack (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}
