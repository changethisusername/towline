package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/internal/gateway"
	"github.com/changethisusername/towline/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func writeProject(t *testing.T, projectsDir, name string, pc map[string]any) string {
	t.Helper()
	dir := filepath.Join(projectsDir, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	raw, _ := json.Marshal(pc)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "towline.json"), raw, 0o600))
	return dir
}

func remoteTestEnv(t *testing.T, portainerURL string) *config.GlobalConfig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skip := false
	cfg := &config.GlobalConfig{PortainerURL: portainerURL, PortainerAPIKey: "ptr_admin", PortainerEnvID: 1,
		ProjectsDir: filepath.Join(home, "projects"), SkipTLSVerify: &skip}
	require.NoError(t, config.SaveGlobalConfig(cfg))
	writeProject(t, cfg.ProjectsDir, "shop", map[string]any{
		"stack_name": "shop-prod", "tier": "prod",
		"mcp_args":       map[string]any{"token": "ptr_shop", "stack": "shop-prod", "tier": "prod"},
		"compose_policy": map[string]any{"mode": "enforce", "allow_networks": []string{"web"}, "allow_volumes": []string{}},
	})
	writeProject(t, cfg.ProjectsDir, "blog", map[string]any{
		"stack_name": "blog-dev", "tier": "dev",
		"mcp_args": map[string]any{"token": "ptr_blog", "stack": "blog-dev", "tier": "dev"},
	})
	return cfg
}

func TestBuildGatewayConfig(t *testing.T) {
	cfg := remoteTestEnv(t, "https://localhost:9443")
	dirs, err := projectDirs(cfg, nil, true)
	require.NoError(t, err)
	rc := &remoteConfig{URL: "https://mcp.example.com", Projects: dirs, OwnerTokenHash: strings.Repeat("ab", 32), TunnelToken: "tun", Approval: gateway.ApprovalAlways}
	gc, err := buildGatewayConfig(cfg, rc)
	require.NoError(t, err)
	require.Len(t, gc.Projects, 2)
	byName := map[string]gateway.ProjectConfig{}
	for _, p := range gc.Projects {
		byName[p.Name] = p
	}
	assert.Equal(t, "prod", byName["shop-prod"].Tier)
	assert.Equal(t, "ptr_shop", byName["shop-prod"].Token)
	assert.Equal(t, []string{"web"}, byName["shop-prod"].ComposePolicy.AllowNetworks)
	assert.Equal(t, "https://host.docker.internal:9443", gc.Portainer.URL, "localhost is the container itself")
	assert.True(t, gc.TrustCloudflare)

	// towline.json is agent-writable: a prod stack can't be relabelled dev.
	writeProject(t, cfg.ProjectsDir, "shop", map[string]any{
		"stack_name": "shop-prod", "tier": "dev",
		"mcp_args": map[string]any{"token": "ptr_shop"},
	})
	_, err = buildGatewayConfig(cfg, rc)
	assert.ErrorContains(t, err, "does not match tier")

	// nor point at another project's stack from its own directory
	writeProject(t, cfg.ProjectsDir, "shop", map[string]any{
		"stack_name": "blog-dev", "tier": "dev",
		"mcp_args": map[string]any{"token": "ptr_shop"},
	})
	_, err = buildGatewayConfig(cfg, rc)
	assert.ErrorContains(t, err, "does not belong")
}

func TestRenderGatewayCompose(t *testing.T) {
	cfg := remoteTestEnv(t, "https://portainer.lan:9443")
	for _, rc := range []*remoteConfig{
		{URL: "https://mcp.example.com", TunnelToken: "tun_s3cret"},
		{URL: "https://mcp.example.com", Port: 8088, Image: "example/towline:1"},
	} {
		out, err := renderGatewayCompose(cfg, rc)
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, yaml.Unmarshal([]byte(out), &doc), out)
		services := doc["services"].(map[string]any)
		gw := services["gateway"].(map[string]any)
		assert.Equal(t, true, gw["read_only"])
		assert.Equal(t, []any{"ALL"}, gw["cap_drop"])
		assert.NotContains(t, out, "tun_s3cret", "secrets stay out of the compose file")
		_, hasTunnel := services["cloudflared"]
		assert.Equal(t, rc.TunnelToken != "", hasTunnel)
		_, hasPorts := gw["ports"]
		assert.Equal(t, rc.Port != 0, hasPorts)
		assert.NotContains(t, out, "host.docker.internal")
	}
}

// fakeStackAPI records stack API calls.
type fakeStackAPI struct {
	mu      sync.Mutex
	stacks  []map[string]any
	created []map[string]any
	updated []map[string]any
}

func (f *fakeStackAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		assert.Equal(t, "ptr_admin", r.Header.Get("X-API-Key"))
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/stacks":
			_ = json.NewEncoder(w).Encode(f.stacks)
		case r.Method == "POST" && r.URL.Path == "/api/stacks/create/standalone/string":
			f.created = append(f.created, m)
			f.stacks = append(f.stacks, map[string]any{"Id": 42, "Name": m["name"], "EndpointId": 1})
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": 42})
		case r.Method == "PUT" && r.URL.Path == "/api/stacks/42":
			f.updated = append(f.updated, m)
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": 42})
		default:
			http.NotFound(w, r)
		}
	}
}

func TestRemoteSetupDeploysThroughPortainer(t *testing.T) {
	fp := &fakeStackAPI{}
	srv := httptest.NewServer(fp.handler(t))
	defer srv.Close()
	remoteTestEnv(t, srv.URL)

	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "tun_secret", "shop", "blog"}))
	require.Len(t, fp.created, 1)
	assert.Equal(t, remoteStackName, fp.created[0]["name"])
	assert.NotContains(t, fp.created[0]["stackFileContent"], "tun_secret")

	env := map[string]string{}
	for _, e := range fp.created[0]["env"].([]any) {
		m := e.(map[string]any)
		env[m["name"].(string)] = m["value"].(string)
	}
	assert.Equal(t, "tun_secret", env["TUNNEL_TOKEN"])
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(env[gateway.ConfigEnvVar], "base64:"))
	require.NoError(t, err)
	gc, err := gateway.ParseConfig(raw)
	require.NoError(t, err, "the deployed config must load in the gateway")
	assert.Len(t, gc.Projects, 2)

	path, _ := remoteConfigPath()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// A second deploy updates the same stack, and the owner token is kept.
	rc1, _ := loadRemoteConfig()
	require.NoError(t, runRemote([]string{"remove", "blog"}))
	require.Len(t, fp.updated, 1)
	rc2, _ := loadRemoteConfig()
	assert.Equal(t, rc1.OwnerTokenHash, rc2.OwnerTokenHash)
	assert.Len(t, rc2.Projects, 1)
}

func TestRemoteComposeDir(t *testing.T) {
	remoteTestEnv(t, "https://portainer.lan:9443")
	out := filepath.Join(t.TempDir(), "gw")
	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "tun", "--compose-dir", out, "--all"}))
	info, err := os.Stat(filepath.Join(out, ".env"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	compose, err := os.ReadFile(filepath.Join(out, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(compose), "cloudflared")
}

func TestRemoteSetupNeedsTunnelOrPort(t *testing.T) {
	remoteTestEnv(t, "https://portainer.lan:9443")
	t.Setenv("TUNNEL_TOKEN", "")
	assert.Error(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--no-deploy", "shop"}))
	assert.Error(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--no-tunnel", "--no-deploy", "shop"}))
	assert.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--no-tunnel", "--port", "8088", "--no-deploy", "shop"}))
	assert.Error(t, runRemote([]string{"setup", "--url", "http://mcp.example.com", "--no-tunnel", "--port", "8088", "--no-deploy", "shop"}), "plain http only for localhost")
}
