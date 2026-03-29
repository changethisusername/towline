package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	deploymentsEnvVar = "_TOWLINE_DEPLOYMENTS"
	maxDeployments    = 50
)

// DeploymentEntry records a single deployment event.
type DeploymentEntry struct {
	ID          int    `json:"id"`
	Timestamp   string `json:"timestamp"`
	Description string `json:"description"`
	Diff        string `json:"diff,omitempty"`
	Outcome     string `json:"outcome"`
}

// HandleDeployments returns a handler for the towline_deployments tool.
// It reads deployment history from the _TOWLINE_DEPLOYMENTS stack env var.
func (h *Handlers) HandleDeployments() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)
		limit, _ := parser.GetInt("limit", false)
		if limit <= 0 {
			limit = 10
		}

		entries, err := h.readDeployments()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to read deployments", err), nil
		}

		// Return only the most recent entries up to limit
		if len(entries) > limit {
			entries = entries[len(entries)-limit:]
		}

		jsonData, err := json.Marshal(entries)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to marshal deployments", err), nil
		}

		return mcp.NewToolResultText(string(jsonData)), nil
	}
}

// RecordDeployment appends a deployment entry to the stack's deployment history.
func (h *Handlers) RecordDeployment(description, previousCompose, newCompose, outcome string) error {
	entries, err := h.readDeployments()
	if err != nil {
		entries = []DeploymentEntry{}
	}

	// Determine next ID
	nextID := 1
	if len(entries) > 0 {
		nextID = entries[len(entries)-1].ID + 1
	}

	diff := computeSimpleDiff(previousCompose, newCompose)

	entry := DeploymentEntry{
		ID:          nextID,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Description: description,
		Diff:        diff,
		Outcome:     outcome,
	}

	entries = append(entries, entry)

	// Cap at maxDeployments
	if len(entries) > maxDeployments {
		entries = entries[len(entries)-maxDeployments:]
	}

	return h.writeDeployments(entries)
}

// readDeployments reads the deployment entries from the stack env var.
func (h *Handlers) readDeployments() ([]DeploymentEntry, error) {
	stacks, err := h.Server.Client().GetLocalStacks()
	if err != nil {
		return nil, fmt.Errorf("failed to get stacks: %w", err)
	}

	var stack *models.LocalStack
	for i := range stacks {
		if stacks[i].Name == h.StackName {
			stack = &stacks[i]
			break
		}
	}
	if stack == nil {
		return nil, fmt.Errorf("stack %q not found", h.StackName)
	}

	for _, env := range stack.Env {
		if env.Name == deploymentsEnvVar {
			var entries []DeploymentEntry
			if err := json.Unmarshal([]byte(env.Value), &entries); err != nil {
				return nil, fmt.Errorf("failed to parse deployments: %w", err)
			}
			return entries, nil
		}
	}

	return []DeploymentEntry{}, nil
}

// writeDeployments writes the deployment entries back to the stack env var.
func (h *Handlers) writeDeployments(entries []DeploymentEntry) error {
	stacks, err := h.Server.Client().GetLocalStacks()
	if err != nil {
		return fmt.Errorf("failed to get stacks: %w", err)
	}

	var stack *models.LocalStack
	for i := range stacks {
		if stacks[i].Name == h.StackName {
			stack = &stacks[i]
			break
		}
	}
	if stack == nil {
		return fmt.Errorf("stack %q not found", h.StackName)
	}

	composeFile, err := h.Server.Client().GetLocalStackFile(stack.ID)
	if err != nil {
		return fmt.Errorf("failed to get compose file: %w", err)
	}

	jsonData, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("failed to marshal deployments: %w", err)
	}

	// Update or add the deployments env var
	envVars := make([]models.LocalStackEnvVar, len(stack.Env))
	copy(envVars, stack.Env)

	found := false
	for i := range envVars {
		if envVars[i].Name == deploymentsEnvVar {
			envVars[i].Value = string(jsonData)
			found = true
			break
		}
	}
	if !found {
		envVars = append(envVars, models.LocalStackEnvVar{
			Name:  deploymentsEnvVar,
			Value: string(jsonData),
		})
	}

	return h.Server.Client().UpdateLocalStack(stack.ID, stack.EndpointID, composeFile, envVars, false, false)
}

// computeSimpleDiff produces a basic line-by-line diff between old and new text.
// Lines only in old are prefixed with "- ", lines only in new with "+ ".
func computeSimpleDiff(old, new string) string {
	if old == new {
		return ""
	}

	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	oldSet := make(map[string]int)
	for _, line := range oldLines {
		oldSet[line]++
	}

	newSet := make(map[string]int)
	for _, line := range newLines {
		newSet[line]++
	}

	var diff strings.Builder

	for _, line := range oldLines {
		if newSet[line] <= 0 {
			diff.WriteString("- ")
			diff.WriteString(line)
			diff.WriteByte('\n')
		} else {
			newSet[line]--
		}
	}

	// Reset newSet for removed lines
	newSet2 := make(map[string]int)
	for _, line := range newLines {
		newSet2[line]++
	}
	for _, line := range oldLines {
		if newSet2[line] > 0 {
			newSet2[line]--
		}
	}

	for _, line := range newLines {
		if newSet2[line] > 0 {
			diff.WriteString("+ ")
			diff.WriteString(line)
			diff.WriteByte('\n')
			newSet2[line]--
		}
	}

	return diff.String()
}
