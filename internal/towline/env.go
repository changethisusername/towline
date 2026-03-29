package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// shouldMaskEnvVar returns true if the variable name suggests it contains a secret.
func shouldMaskEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	sensitivePatterns := []string{
		"KEY", "SECRET", "PASSWORD", "TOKEN",
		"PASSPHRASE", "PRIVATE", "CREDENTIAL",
		"API_KEY", "APIKEY",
	}
	for _, pattern := range sensitivePatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// envVarDisplay is the JSON representation of an environment variable for display.
type envVarDisplay struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HandleEnvGet returns a handler for the towline_env_get tool.
// It reads stack environment variables, masks sensitive values, and skips internal _TOWLINE_* vars.
func (h *Handlers) HandleEnvGet() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)
		filterName, _ := parser.GetString("name", false)

		stacks, err := h.Server.Client().GetLocalStacks()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get stacks", err), nil
		}

		// Find our stack
		var stack *models.LocalStack
		for i := range stacks {
			if stacks[i].Name == h.StackName {
				stack = &stacks[i]
				break
			}
		}
		if stack == nil {
			return mcp.NewToolResultError(fmt.Sprintf("stack %q not found", h.StackName)), nil
		}

		var result []envVarDisplay
		for _, env := range stack.Env {
			// Skip internal towline variables
			if strings.HasPrefix(env.Name, "_TOWLINE_") {
				continue
			}

			// Filter by name if specified
			if filterName != "" && env.Name != filterName {
				continue
			}

			value := env.Value
			if shouldMaskEnvVar(env.Name) {
				if len(value) > 4 {
					value = value[:4] + "****"
				} else {
					value = "****"
				}
			}

			result = append(result, envVarDisplay{
				Name:  env.Name,
				Value: value,
			})
		}

		if result == nil {
			result = []envVarDisplay{}
		}

		jsonData, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to marshal env vars", err), nil
		}

		return mcp.NewToolResultText(string(jsonData)), nil
	}
}

// HandleEnvSet returns a handler for the towline_env_set tool.
// It reads the current env, updates/adds the specified variable, then writes back.
func (h *Handlers) HandleEnvSet() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		name, err := parser.GetString("name", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid name parameter", err), nil
		}

		value, err := parser.GetString("value", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("invalid value parameter", err), nil
		}

		// Prevent setting internal vars
		if strings.HasPrefix(name, "_TOWLINE_") {
			return mcp.NewToolResultError("cannot set internal _TOWLINE_* variables"), nil
		}

		// Get current stacks
		stacks, err := h.Server.Client().GetLocalStacks()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get stacks", err), nil
		}

		// Find our stack
		var stack *models.LocalStack
		for i := range stacks {
			if stacks[i].Name == h.StackName {
				stack = &stacks[i]
				break
			}
		}
		if stack == nil {
			return mcp.NewToolResultError(fmt.Sprintf("stack %q not found", h.StackName)), nil
		}

		// Get the current compose file (must preserve it during update)
		composeFile, err := h.Server.Client().GetLocalStackFile(stack.ID)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get stack compose file", err), nil
		}

		// Update or add the env var
		found := false
		envVars := make([]models.LocalStackEnvVar, len(stack.Env))
		copy(envVars, stack.Env)

		for i := range envVars {
			if envVars[i].Name == name {
				envVars[i].Value = value
				found = true
				break
			}
		}
		if !found {
			envVars = append(envVars, models.LocalStackEnvVar{Name: name, Value: value})
		}

		// Write back via UpdateLocalStack, preserving compose file
		err = h.Server.Client().UpdateLocalStack(stack.ID, stack.EndpointID, composeFile, envVars, false, false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to update stack env", err), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Environment variable %q set successfully", name)), nil
	}
}
