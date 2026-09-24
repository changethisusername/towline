package cli

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/changethisusername/towline/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyMCPJSON is what earlier releases generated, plus a user edit
// (-proxy caddy) and a second MCP server the user added.
const legacyMCPJSON = `{
  "mcpServers": {
    "towline-prod": {
      "type": "stdio",
      "command": "towline-mcp",
      "args": ["-server", "https://p:9443", "-token", "ptr_legacy", "-stack", "shop-prod", "-tier", "prod",
               "-disable-version-check", "-skip-tls-verify", "-proxy", "caddy", "-caddy-api", "http://caddy:2019"]
    },
    "github": {"command": "gh-mcp", "args": []}
  }
}
`

func setupLegacyProject(t *testing.T, portainerURL string, cfg config.GlobalConfig) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg.PortainerURL = portainerURL
	cfg.PortainerAPIKey = "ptr_admin"
	cfg.PortainerEnvID = 1
	cfg.ProjectsDir = filepath.Join(home, "projects")
	require.NoError(t, config.SaveGlobalConfig(&cfg))

	dir := filepath.Join(cfg.ProjectsDir, "shop")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(legacyMCPJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude", "settings.json"),
		[]byte(`{"permissions":{"allow":["Bash(npm test)"]},"model":"x"}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".claude/\n"), 0644))

	// towline.json as written by earlier releases (no compose_policy)
	pc := map[string]any{
		"stack_name": "shop-prod", "stack_id": 11, "tier": "prod", "team_id": 7, "user_id": 9,
		"environment_id": 1, "mcp_binary": "towline-mcp",
		"mcp_args": map[string]any{"server": "https://p:9443", "token": "ptr_legacy", "stack": "shop-prod", "tier": "prod"},
	}
	raw, _ := json.Marshal(pc)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "towline.json"), raw, 0644))
	return dir
}

func readMCPServer(t *testing.T, file, name string) (args []string, env map[string]string, servers map[string]json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	var entry struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
	}
	require.NoError(t, json.Unmarshal(cfg.MCPServers[name], &entry))
	return entry.Args, entry.Env, cfg.MCPServers
}

func TestRefresh_LegacyProject(t *testing.T) {
	f := newFakePortainer()
	f.teams[7] = "team-shop-prod"
	f.stackFiles = map[int]string{11: `services:
  web:
    image: nginx
    volumes:
      - /srv/shop/uploads:/uploads
      - ./config:/config
      - /var/run/docker.sock:/var/run/docker.sock
      - data:/data
volumes:
  data:
`}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	// Legacy global config: no skip_tls_verify, no approval block.
	dir := setupLegacyProject(t, srv.URL, config.GlobalConfig{})
	require.NoError(t, runRefresh([]string{"shop"}))

	args, env, servers := readMCPServer(t, filepath.Join(dir, ".mcp.json"), "towline-prod")

	// Token moved out of argv; legacy behaviors preserved.
	assert.Equal(t, "ptr_legacy", env["TOWLINE_PORTAINER_TOKEN"])
	assert.NotContains(t, args, "-token")
	assert.NotContains(t, args, "ptr_legacy")
	assert.Contains(t, args, "-skip-tls-verify", "legacy config keeps skipping TLS verification")
	assert.Equal(t, "agent", argValue(args, "-approval-mode"), "legacy config keeps agent confirmation")

	// User edits and other servers survive.
	assert.Equal(t, "caddy", argValue(args, "-proxy"))
	assert.Equal(t, "http://caddy:2019", argValue(args, "-caddy-api"))
	assert.Contains(t, servers, "github")

	// Existing bind mounts are grandfathered, except docker.sock.
	assert.Equal(t, "/srv/shop/uploads,.", argValue(args, "-allow-bind-mounts"))
	pc, err := config.LoadProjectConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, pc.ComposePolicy)
	assert.Equal(t, []string{"/srv/shop/uploads", "."}, pc.ComposePolicy.AllowBindMounts)

	// Claude settings keep user content and gain the MCP permission.
	raw, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	assert.Contains(t, string(raw), "Bash(npm test)")
	assert.Contains(t, string(raw), "mcp__towline-prod")
	assert.Contains(t, string(raw), `"model": "x"`)

	// Permissions and .gitignore.
	for _, p := range []string{".mcp.json", ".claude/settings.json", "towline.json"} {
		info, err := os.Stat(filepath.Join(dir, p))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), p)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	assert.Contains(t, string(gi), ".mcp.json\n")

	// Team moved to the Standard user role.
	require.Len(t, f.endpointPuts, 1)
	assert.Contains(t, f.endpointPuts[0], `"RoleId":4`)

	// A second refresh is stable and doesn't re-grandfather.
	before, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	require.NoError(t, runRefresh([]string{"--keep-role", "shop"}))
	after, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	assert.Equal(t, string(before), string(after))
	assert.Len(t, f.endpointPuts, 1)
}

func TestRefresh_HumanApprovalConfig(t *testing.T) {
	f := newFakePortainer()
	f.teams[7] = "team-shop-prod"
	f.stackFiles = map[int]string{11: "services:\n  web:\n    image: nginx\n"}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	gc := config.GlobalConfig{SkipTLSVerify: boolPtr(false)}
	_, err := configureApprovals(&gc, approvalOptions{Mode: "human"})
	require.NoError(t, err)
	dir := setupLegacyProject(t, srv.URL, gc)

	require.NoError(t, runRefresh([]string{"shop"}))
	args, env, _ := readMCPServer(t, filepath.Join(dir, ".mcp.json"), "towline-prod")
	assert.Equal(t, "human", argValue(args, "-approval-mode"))
	assert.Equal(t, "http://127.0.0.1:8787", argValue(args, "-approval-webhook"))
	assert.Equal(t, gc.Approval.WebhookToken, env["TOWLINE_APPROVAL_WEBHOOK_TOKEN"])
	assert.NotContains(t, args, "-skip-tls-verify")
	assert.NotContains(t, args, "-allow-bind-mounts")
}

func TestRefresh_TeamNameMismatchSkipsRole(t *testing.T) {
	f := newFakePortainer()
	f.teams[7] = "team-someone-else"
	f.stackFiles = map[int]string{11: "services: {}\n"}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	setupLegacyProject(t, srv.URL, config.GlobalConfig{})
	require.NoError(t, runRefresh([]string{"shop"}))
	assert.Empty(t, f.endpointPuts)
}

func TestConfigureApprovals(t *testing.T) {
	var cfg config.GlobalConfig
	token, err := configureApprovals(&cfg, approvalOptions{Mode: "human", Listen: "0.0.0.0:9000"})
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, "http://127.0.0.1:9000", cfg.Approval.URL)
	assert.NotEqual(t, token, cfg.Approval.ApproverTokenHash, "only the hash is stored")
	webhookToken := cfg.Approval.WebhookToken
	assert.NotEmpty(t, webhookToken)

	// Re-running setup rotates the approver token but keeps the webhook
	// token, so existing project configs stay valid.
	token2, err := configureApprovals(&cfg, approvalOptions{Mode: "human"})
	require.NoError(t, err)
	assert.NotEqual(t, token, token2)
	assert.Equal(t, webhookToken, cfg.Approval.WebhookToken)

	_, err = configureApprovals(&cfg, approvalOptions{Mode: "human", ExternalURL: "https://approvals.example", WebhookToken: "ext"})
	require.NoError(t, err)
	assert.Equal(t, "https://approvals.example", cfg.Approval.URL)
	assert.Empty(t, cfg.Approval.ApproverTokenHash)

	_, err = configureApprovals(&cfg, approvalOptions{Mode: "agent"})
	require.NoError(t, err)
	assert.Equal(t, "agent", cfg.Approval.EffectiveMode())

	_, err = configureApprovals(&cfg, approvalOptions{Mode: "robot"})
	assert.Error(t, err)
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestCarryOver_ValueFlags(t *testing.T) {
	old := map[string]any{"args": []any{"-token", "x", "-read-only", "-proxy", "traefik", "-tools=/t.yaml"}, "env": map[string]any{"FOO": "1"}}
	entry := map[string]any{"args": []any{"-server", "s"}, "env": map[string]any{"TOWLINE_PORTAINER_TOKEN": "t"}}
	carryOver(old, entry)
	var got []string
	for _, a := range entry["args"].([]any) {
		got = append(got, a.(string))
	}
	assert.Equal(t, "-server s -read-only -proxy traefik -tools=/t.yaml", strings.Join(got, " "))
	assert.Equal(t, "1", entry["env"].(map[string]any)["FOO"])
}
