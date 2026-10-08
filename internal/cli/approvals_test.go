package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTerminalSafe(t *testing.T) {
	tests := []struct {
		name, in, want, wantBlock string
	}{
		{"plain", "updateLocalStack", "updateLocalStack", "updateLocalStack"},
		{"ansi escape", "a\x1b[2Kb", "a?[2Kb", "a?[2Kb"},
		{"newline", "a\nb", "a?b", "a\nb"},
		{"bidi override", "a‮b", "a?b", "a?b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, terminalSafe(tt.in))
			assert.Equal(t, tt.wantBlock, terminalSafeBlock(tt.in))
		})
	}
}
