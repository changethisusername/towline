package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureEnv(got *[]interface{}) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		*got, _ = req.GetArguments()["env"].([]interface{})
		return mcp.NewToolResultText("executed"), nil
	}
}

func envNames(env []interface{}) []string {
	var names []string
	for _, e := range env {
		names = append(names, e.(map[string]interface{})["name"].(string))
	}
	return names
}

func TestEnvFilter(t *testing.T) {
	prepare := func(newCompose string) ([]models.LocalStackEnvVar, []models.LocalStackEnvVar, error) {
		assert.Equal(t, "services: {}\n", newCompose)
		return []models.LocalStackEnvVar{{Name: "EXISTING", Value: "1"}},
			[]models.LocalStackEnvVar{{Name: "_TOWLINE_DEPLOYMENTS", Value: "[...]"}}, nil
	}

	tests := []struct {
		name    string
		tool    string
		prepare StackUpdatePreparer
		env     []interface{}
		want    []string
	}{
		{
			name: "create strips internal vars",
			tool: "createLocalStack",
			env: []interface{}{
				map[string]interface{}{"name": "A", "value": "1"},
				map[string]interface{}{"name": "_TOWLINE_DEPLOYMENTS", "value": "[]"},
			},
			want: []string{"A"},
		},
		{
			name:    "update replaces agent internal vars with stack's",
			tool:    "updateLocalStack",
			prepare: prepare,
			env: []interface{}{
				map[string]interface{}{"name": "A", "value": "1"},
				map[string]interface{}{"name": "_TOWLINE_DEPLOYMENTS", "value": "[]"},
			},
			want: []string{"A", "_TOWLINE_DEPLOYMENTS"},
		},
		{
			name:    "update without env keeps current env",
			tool:    "updateLocalStack",
			prepare: prepare,
			env:     nil,
			want:    []string{"EXISTING", "_TOWLINE_DEPLOYMENTS"},
		},
		{
			name:    "update with explicit empty env clears user env only",
			tool:    "updateLocalStack",
			prepare: prepare,
			env:     []interface{}{},
			want:    []string{"_TOWLINE_DEPLOYMENTS"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"file": "services: {}\n"}
			if tt.env != nil {
				args["env"] = tt.env
			}
			var got []interface{}
			handler := NewEnvFilter(tt.tool, tt.prepare)(captureEnv(&got))
			_, err := handler(context.Background(), makeRequest(args))
			require.NoError(t, err)
			assert.Equal(t, tt.want, envNames(got))
		})
	}
}

func TestEnvFilter_PrepareErrorFailsUpdate(t *testing.T) {
	prepare := func(string) ([]models.LocalStackEnvVar, []models.LocalStackEnvVar, error) {
		return nil, nil, errors.New("boom")
	}
	called := false
	handler := NewEnvFilter("updateLocalStack", prepare)(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return nil, nil
	})
	result, err := handler(context.Background(), makeRequest(map[string]any{"file": "x"}))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.False(t, called)
}
