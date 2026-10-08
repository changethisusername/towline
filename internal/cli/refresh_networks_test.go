package cli

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/changethisusername/towline/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const networksCompose = `services:
  web:
    image: nginx
    networks: [proxy, public, hostnet, internal]
networks:
  proxy:
    external: true
  public:
    name: traefik_public
  hostnet:
    name: host
    external: true
  internal:
`

func TestRefresh_GrandfathersExternalNetworks(t *testing.T) {
	tests := []struct {
		name           string
		existingPolicy *config.ComposePolicyConfig
		wantNetworks   []string
		wantArg        string
	}{
		{
			name:         "first refresh",
			wantNetworks: []string{"proxy", "traefik_public"},
			wantArg:      "proxy,traefik_public",
		},
		{
			name:           "policy predating allow_networks",
			existingPolicy: &config.ComposePolicyConfig{Mode: "enforce", AllowBindMounts: []string{"/srv/x"}},
			wantNetworks:   []string{"proxy", "traefik_public"},
			wantArg:        "proxy,traefik_public",
		},
		{
			name:           "already grandfathered is left alone",
			existingPolicy: &config.ComposePolicyConfig{Mode: "enforce", AllowNetworks: []string{}},
			wantNetworks:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakePortainer()
			f.teams[7] = "team-shop-prod"
			f.stackFiles = map[int]string{11: networksCompose}
			f.stacks[11] = config.StackInfo{ID: 11, Name: "shop-prod", EndpointID: 1}
			srv := httptest.NewServer(f.handler(t))
			defer srv.Close()

			dir := setupLegacyProject(t, srv.URL, config.GlobalConfig{})
			if tt.existingPolicy != nil {
				pc, err := config.LoadProjectConfig(dir)
				require.NoError(t, err)
				pc.ComposePolicy = tt.existingPolicy
				require.NoError(t, config.SaveProjectConfig(dir, pc))
			}

			require.NoError(t, runRefresh([]string{"--keep-role", "shop"}))
			pc, err := config.LoadProjectConfig(dir)
			require.NoError(t, err)
			require.NotNil(t, pc.ComposePolicy)
			assert.Equal(t, tt.wantNetworks, pc.ComposePolicy.AllowNetworks, "host is never grandfathered")
			if tt.existingPolicy != nil {
				assert.Equal(t, tt.existingPolicy.AllowBindMounts, pc.ComposePolicy.AllowBindMounts)
			}

			args, _, _ := readMCPServer(t, filepath.Join(dir, ".mcp.json"), "towline-prod")
			if tt.wantArg == "" {
				assert.NotContains(t, args, "-allow-networks")
			} else {
				assert.Equal(t, tt.wantArg, argValue(args, "-allow-networks"))
			}

			// Networks added to the stack later are not grandfathered.
			f.stackFiles[11] = networksCompose + "  extra:\n    external: true\n"
			require.NoError(t, runRefresh([]string{"--keep-role", "shop"}))
			pc, err = config.LoadProjectConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantNetworks, pc.ComposePolicy.AllowNetworks)
		})
	}
}

func TestRefresh_GrandfathersExternalVolumes(t *testing.T) {
	const compose = `services:
  db:
    image: postgres
    volumes: [data:/var/lib/postgresql/data, cache:/cache, own:/own]
volumes:
  data:
    name: legacy_data
  cache:
    external: true
  own:
    name: shop-prod_own
`
	f := newFakePortainer()
	f.teams[7] = "team-shop-prod"
	f.stackFiles = map[int]string{11: compose}
	f.stacks[11] = config.StackInfo{ID: 11, Name: "shop-prod", EndpointID: 1}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	// A policy written before allow_volumes existed.
	dir := setupLegacyProject(t, srv.URL, config.GlobalConfig{})
	pc, err := config.LoadProjectConfig(dir)
	require.NoError(t, err)
	pc.ComposePolicy = &config.ComposePolicyConfig{Mode: "enforce", AllowNetworks: []string{}}
	require.NoError(t, config.SaveProjectConfig(dir, pc))

	require.NoError(t, runRefresh([]string{"--keep-role", "shop"}))
	pc, err = config.LoadProjectConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"cache", "legacy_data", "shop-prod_own"}, pc.ComposePolicy.AllowVolumes)
	assert.Equal(t, []string{}, pc.ComposePolicy.AllowNetworks)

	args, _, _ := readMCPServer(t, filepath.Join(dir, ".mcp.json"), "towline-prod")
	assert.Equal(t, "cache,legacy_data,shop-prod_own", argValue(args, "-allow-volumes"))
}
