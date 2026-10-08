package towline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEnvVarName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"simple upper", "DATABASE_URL", false},
		{"leading underscore", "_PRIVATE", false},
		{"lowercase and digits", "node_env2", false},
		{"empty", "", true},
		{"contains equals", "FOO=BAR", true},
		{"contains space", "FOO BAR", true},
		{"contains newline", "FOO\nBAR=1", true},
		{"trailing tab", "FOO\t", true},
		{"leading digit", "1FOO", true},
		{"dash", "FOO-BAR", true},
		{"dollar", "FOO$", true},
		{"non-ASCII letter", "FÖO", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvVarName(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHandleEnvSet_RejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name    string
		varName string
	}{
		{"equals sign", "A=B"},
		{"newline injection", "A\nB"},
		{"whitespace", "A B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No client expectations: the handler must refuse before
			// touching the stack.
			cli := new(mockPortainerClient)
			h := setupHandlers(t, cli, nil)
			result, err := h.HandleEnvSet()(context.Background(), newRequest(map[string]any{
				"name":  tt.varName,
				"value": "x",
			}))
			require.NoError(t, err)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "invalid variable name")
			cli.AssertExpectations(t)
		})
	}
}

func TestShouldMaskEnvVar(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"API_KEY", "API_KEY", true},
		{"api_key lowercase", "api_key", true},
		{"SECRET_VALUE", "SECRET_VALUE", true},
		{"DB_PASSWORD", "DB_PASSWORD", true},
		{"AUTH_TOKEN", "AUTH_TOKEN", true},
		{"mixed case token", "Auth_Token", true},
		{"DATABASE_URL", "DATABASE_URL", false},
		{"APP_NAME", "APP_NAME", false},
		{"PORT", "PORT", false},
		{"NODE_ENV", "NODE_ENV", false},
		{"empty string", "", false},
		{"KEYSTORE_PATH contains KEY", "KEYSTORE_PATH", true},
		{"password in middle", "MY_PASSWORD_HERE", true},
		{"secret in middle", "MY_SECRET_STUFF", true},
		{"tokenizer", "TOKENIZER_PATH", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldMaskEnvVar(tt.input)
			assert.Equal(t, tt.expected, result, "shouldMaskEnvVar(%q)", tt.input)
		})
	}
}
