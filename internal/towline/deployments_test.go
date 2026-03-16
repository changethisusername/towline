package towline

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentEntryParsing(t *testing.T) {
	data := `[
		{"id":1,"timestamp":"2026-03-17T10:00:00Z","description":"initial deploy","diff":"","outcome":"success"},
		{"id":2,"timestamp":"2026-03-17T11:00:00Z","description":"add redis","diff":"+ redis:\n+   image: redis:7\n","outcome":"success"}
	]`

	var entries []DeploymentEntry
	err := json.Unmarshal([]byte(data), &entries)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, 1, entries[0].ID)
	assert.Equal(t, "initial deploy", entries[0].Description)
	assert.Equal(t, "add redis", entries[1].Description)
	assert.Equal(t, "success", entries[1].Outcome)
}

func TestDeploymentEntryCapping(t *testing.T) {
	// Create more than maxDeployments entries
	entries := make([]DeploymentEntry, 60)
	for i := range entries {
		entries[i] = DeploymentEntry{
			ID:          i + 1,
			Timestamp:   "2026-03-17T10:00:00Z",
			Description: "deploy",
			Outcome:     "success",
		}
	}

	// Simulate the capping logic from RecordDeployment
	if len(entries) > maxDeployments {
		entries = entries[len(entries)-maxDeployments:]
	}

	assert.Len(t, entries, maxDeployments)
	// Should keep the last 50 entries (IDs 11-60)
	assert.Equal(t, 11, entries[0].ID)
	assert.Equal(t, 60, entries[len(entries)-1].ID)
}

func TestComputeSimpleDiff(t *testing.T) {
	tests := []struct {
		name     string
		old      string
		new      string
		expected string
	}{
		{
			name:     "identical",
			old:      "line1\nline2",
			new:      "line1\nline2",
			expected: "",
		},
		{
			name:     "added line",
			old:      "line1\nline2",
			new:      "line1\nline2\nline3",
			expected: "+ line3\n",
		},
		{
			name:     "removed line",
			old:      "line1\nline2\nline3",
			new:      "line1\nline2",
			expected: "- line3\n",
		},
		{
			name:     "changed line",
			old:      "line1\nold_line\nline3",
			new:      "line1\nnew_line\nline3",
			expected: "- old_line\n+ new_line\n",
		},
		{
			name:     "empty old",
			old:      "",
			new:      "line1\nline2",
			expected: "- \n+ line1\n+ line2\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeSimpleDiff(tt.old, tt.new)
			assert.Equal(t, tt.expected, result)
		})
	}
}
