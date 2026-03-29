package approval

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStore(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	require.NotNil(t, s)
	require.NotNil(t, s.pending)
	require.NotNil(t, s.stopCh)
}

func TestStore_RequestAndValidate(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	args := map[string]any{"file": "/tmp/test.txt"}
	token := s.Request("write_file", args)

	require.NotEmpty(t, token)

	p := s.Validate(token, args)
	require.NotNil(t, p)
	assert.Equal(t, "write_file", p.ToolName)
	assert.Equal(t, args, p.Args)
	assert.WithinDuration(t, time.Now(), p.CreatedAt, 5*time.Second)
}

func TestStore_ValidateConsumesToken(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	token := s.Request("some_tool", nil)

	// First validation succeeds
	p := s.Validate(token, nil)
	require.NotNil(t, p)

	// Second validation returns nil (token consumed)
	p = s.Validate(token, nil)
	assert.Nil(t, p)
}

func TestStore_InvalidToken(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	p := s.Validate("nonexistent_token", nil)
	assert.Nil(t, p)
}

func TestStore_ExpiredToken(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	token := s.Request("dangerous_tool", map[string]any{"action": "delete"})

	// Manually set CreatedAt to the past (beyond TTL)
	s.mu.Lock()
	s.pending[token].CreatedAt = time.Now().Add(-(TokenTTL + time.Minute))
	s.mu.Unlock()

	p := s.Validate(token, map[string]any{"action": "delete"})
	assert.Nil(t, p, "expired token should return nil")
}

func TestStore_MultipleTokens(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	token1 := s.Request("tool_a", map[string]any{"x": 1})
	token2 := s.Request("tool_b", map[string]any{"y": 2})
	token3 := s.Request("tool_c", map[string]any{"z": 3})

	// Validate in reverse order
	p3 := s.Validate(token3, map[string]any{"z": 3})
	require.NotNil(t, p3)
	assert.Equal(t, "tool_c", p3.ToolName)

	p1 := s.Validate(token1, map[string]any{"x": 1})
	require.NotNil(t, p1)
	assert.Equal(t, "tool_a", p1.ToolName)

	p2 := s.Validate(token2, map[string]any{"y": 2})
	require.NotNil(t, p2)
	assert.Equal(t, "tool_b", p2.ToolName)

	// All consumed
	assert.Nil(t, s.Validate(token1, map[string]any{"x": 1}))
	assert.Nil(t, s.Validate(token2, map[string]any{"y": 2}))
	assert.Nil(t, s.Validate(token3, map[string]any{"z": 3}))
}

func TestStore_Stop(t *testing.T) {
	s := NewStore()

	// Request a token before stopping
	token := s.Request("tool", nil)

	// Stop should not panic
	s.Stop()

	// Existing token should still be retrievable
	p := s.Validate(token, nil)
	require.NotNil(t, p)
	assert.Equal(t, "tool", p.ToolName)
}

func TestGenerateToken(t *testing.T) {
	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		tok := generateToken()

		// Verify format: 32 hex characters (16 bytes = 32 hex digits)
		assert.Len(t, tok, 32, "token should be 32 hex chars")
		_, err := hex.DecodeString(tok)
		assert.NoError(t, err, "token should be valid hex")

		// Verify uniqueness
		assert.False(t, seen[tok], "token should be unique, got duplicate: %s", tok)
		seen[tok] = true
	}
}
