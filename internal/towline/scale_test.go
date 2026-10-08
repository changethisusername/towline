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

func TestSetServiceReplicas_AnchorsAndMergeKeys(t *testing.T) {
	tests := []struct {
		name         string
		compose      string
		service      string
		wantErr      string
		wantContains []string
	}{
		{
			name: "plain service gets deploy.replicas",
			compose: `services:
  web:
    image: nginx
`,
			service:      "web",
			wantContains: []string{"deploy:\n            replicas: 3"},
		},
		{
			name: "deploy is an alias shared with another service",
			compose: `x-deploy: &d
  resources:
    limits:
      memory: 256m
services:
  web:
    image: nginx
    deploy: *d
  api:
    image: api
    deploy: *d
`,
			service: "web",
			wantErr: "YAML alias",
		},
		{
			name: "deploy is anchored and reused by another service",
			compose: `services:
  web:
    image: nginx
    deploy: &d
      replicas: 1
  api:
    image: api
    deploy: *d
`,
			service: "web",
			wantErr: "anchored (&d)",
		},
		{
			name: "deploy inherited via merge key would be replaced",
			compose: `x-base: &base
  image: nginx
  deploy:
    resources:
      limits:
        memory: 256m
services:
  web:
    <<: *base
`,
			service: "web",
			wantErr: "merge key",
		},
		{
			name: "deploy inherited via merge key list",
			compose: `x-a: &a
  image: nginx
x-b: &b
  deploy:
    resources:
      limits:
        cpus: "0.5"
services:
  web:
    <<: [*a, *b]
`,
			service: "web",
			wantErr: "merge key",
		},
		{
			name: "service anchored and merged into another service",
			compose: `services:
  web: &web
    image: nginx
  web2:
    <<: *web
    ports:
      - "8080:80"
`,
			service: "web",
			wantErr: "reused by other services",
		},
		{
			name: "service is itself an alias",
			compose: `x-svc: &svc
  image: nginx
services:
  web: *svc
`,
			service: "web",
			wantErr: "YAML alias",
		},
		{
			name: "merge key without deploy is fine",
			compose: `x-base: &base
  image: nginx
  restart: always
services:
  web:
    <<: *base
`,
			service:      "web",
			wantContains: []string{"<<: *base", "replicas: 3"},
		},
		{
			name: "own deploy overriding a merged one is fine",
			compose: `x-base: &base
  image: nginx
  deploy:
    replicas: 1
services:
  web:
    <<: *base
    deploy:
      replicas: 1
      resources:
        limits:
          memory: 128m
`,
			service:      "web",
			wantContains: []string{"replicas: 3", "memory: 128m"},
		},
		{
			name: "anchored deploy that nobody reuses is fine",
			compose: `services:
  web:
    image: nginx
    deploy: &d
      replicas: 1
`,
			service:      "web",
			wantContains: []string{"replicas: 3"},
		},
		{
			name: "replicas value is an alias",
			compose: `x-n: &n 2
services:
  web:
    image: nginx
    deploy:
      replicas: *n
`,
			service: "web",
			wantErr: "deploy.replicas",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := setServiceReplicas(tt.compose, tt.service, 3)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			for _, s := range tt.wantContains {
				assert.Contains(t, out, s)
			}
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
