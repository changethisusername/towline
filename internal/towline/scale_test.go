package towline

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateReplicas(t *testing.T) {
	tests := []struct {
		name    string
		in      float64
		want    int
		wantErr string
	}{
		{name: "zero stops the service", in: 0, want: 0},
		{name: "one", in: 1, want: 1},
		{name: "at the cap", in: maxReplicas, want: maxReplicas},
		{name: "integral float", in: 3.0, want: 3},
		{name: "fraction below one is not truncated to zero", in: 0.5, wantErr: "whole number"},
		{name: "fraction above one", in: 2.7, wantErr: "whole number"},
		{name: "negative", in: -1, wantErr: ">= 0"},
		{name: "above the cap", in: maxReplicas + 1, wantErr: "<= 20"},
		{name: "huge", in: 1e12, wantErr: "<= 20"},
		{name: "NaN", in: math.NaN(), wantErr: "whole number"},
		{name: "infinity", in: math.Inf(1), wantErr: "whole number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateReplicas(tt.in)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandleScale_RejectsInvalidReplicas(t *testing.T) {
	tests := []struct {
		name     string
		replicas any
		wantErr  string
	}{
		{name: "fractional", replicas: float64(0.5), wantErr: "whole number"},
		{name: "over the cap", replicas: float64(1000), wantErr: "<= 20"},
		{name: "not a number", replicas: "3", wantErr: "must be a number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No client expectations: the stack must not be touched.
			cli := new(mockPortainerClient)
			h := setupHandlers(t, cli, nil)
			result, err := h.HandleScale()(context.Background(), newRequest(map[string]any{
				"service":  "web",
				"replicas": tt.replicas,
			}))
			require.NoError(t, err)
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), tt.wantErr)
			cli.AssertExpectations(t)
		})
	}
}
