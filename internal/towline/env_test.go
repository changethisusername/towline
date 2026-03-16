package towline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
