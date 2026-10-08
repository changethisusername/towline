package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCaddyUpstreamHost(t *testing.T) {
	tests := []struct {
		stack, service, want string
	}{
		{"shop", "web", "web.shop.towline"},
		// The pairs that collide under "<stack>-<service>" stay distinct.
		{"a", "b-c", "b-c.a.towline"},
		{"a-b", "c", "c.a-b.towline"},
	}
	for _, tt := range tests {
		t.Run(tt.stack+"/"+tt.service, func(t *testing.T) {
			assert.Equal(t, tt.want, CaddyUpstreamHost(tt.stack, tt.service))
		})
	}
}

func TestWithNetworkAlias(t *testing.T) {
	const alias = "web.shop.towline"

	// networksOf returns service web's networks -> aliases after the edit.
	networksOf := func(t *testing.T, compose string) map[string][]string {
		t.Helper()
		var doc struct {
			Services map[string]struct {
				Networks map[string]*struct {
					Aliases []string `yaml:"aliases"`
				} `yaml:"networks"`
			} `yaml:"services"`
		}
		require.NoError(t, yaml.Unmarshal([]byte(compose), &doc))
		out := map[string][]string{}
		for name, cfg := range doc.Services["web"].Networks {
			if cfg == nil {
				out[name] = nil
				continue
			}
			out[name] = cfg.Aliases
		}
		return out
	}

	tests := []struct {
		name      string
		compose   string
		want      map[string][]string
		unchanged bool
		wantErr   string
	}{
		{
			name: "no networks uses the default network",
			compose: `services:
  web:
    image: nginx
`,
			want: map[string][]string{"default": {alias}},
		},
		{
			name: "short form list is converted",
			compose: `services:
  web:
    image: nginx
    networks:
      - default
      - caddy
networks:
  caddy:
    external: true
`,
			want: map[string][]string{"default": {alias}, "caddy": {alias}},
		},
		{
			name: "long form keeps existing settings and aliases",
			compose: `services:
  web:
    image: nginx
    networks:
      caddy:
        aliases:
          - mine
        ipv4_address: 172.20.0.5
      backend:
networks:
  caddy:
    external: true
  backend: {}
`,
			want: map[string][]string{"caddy": {"mine", alias}, "backend": {alias}},
		},
		{
			name: "already aliased is unchanged",
			compose: `services:
  web:
    image: nginx
    networks:
      caddy:
        aliases:
          - web.shop.towline
`,
			unchanged: true,
		},
		{
			name: "network_mode is refused",
			compose: `services:
  web:
    image: nginx
    network_mode: host
`,
			wantErr: "network_mode",
		},
		{
			name: "networks alias is refused",
			compose: `x-nets: &n
  - caddy
services:
  web:
    image: nginx
    networks: *n
`,
			wantErr: "YAML alias",
		},
		{
			name: "networks inherited through merge key are refused",
			compose: `x-base: &base
  image: nginx
  networks:
    - caddy
services:
  web:
    <<: *base
`,
			wantErr: "merge key",
		},
		{
			name: "shared network config is refused",
			compose: `x-net: &net
  aliases:
    - shared
services:
  web:
    image: nginx
    networks:
      caddy: *net
`,
			wantErr: "anchor or alias",
		},
		{
			name: "unknown service",
			compose: `services:
  api:
    image: nginx
`,
			wantErr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := withNetworkAlias(tt.compose, "web", alias)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.unchanged {
				assert.Equal(t, tt.compose, out)
				return
			}
			assert.Equal(t, tt.want, networksOf(t, out))
		})
	}
}
