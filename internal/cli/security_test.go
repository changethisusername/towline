package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePortainer is a minimal Portainer API for provisioning/destroy tests.
type fakePortainer struct {
	mu       sync.Mutex
	failStep string // path prefix ("METHOD /path") whose request fails
	deleted  []string
	teams    map[int]string
	users    map[int]string
	stacks   map[int]config.StackInfo
	requests []string
	// authTokens records the auth header used for each DELETE
	deleteAuth []string
}

func newFakePortainer() *fakePortainer {
	return &fakePortainer{teams: map[int]string{}, users: map[int]string{}, stacks: map[int]config.StackInfo{}}
}

func (f *fakePortainer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		f.requests = append(f.requests, key)
		if f.failStep != "" && strings.HasPrefix(key, f.failStep) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("injected failure"))
			return
		}
		writeJSON := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		var id int
		switch {
		case key == "POST /api/teams":
			f.teams[7] = "created"
			writeJSON(map[string]any{"Id": 7})
		case key == "GET /api/endpoints/1":
			writeJSON(map[string]any{"Id": 1, "TeamAccessPolicies": map[string]any{}})
		case key == "PUT /api/endpoints/1":
			writeJSON(map[string]any{})
		case key == "POST /api/users":
			f.users[9] = "created"
			writeJSON(map[string]any{"Id": 9})
		case key == "POST /api/team_memberships":
			writeJSON(map[string]any{})
		case key == "POST /api/auth":
			writeJSON(map[string]any{"jwt": "user.jwt.token"})
		case key == "POST /api/users/9/tokens":
			writeJSON(map[string]any{"rawAPIKey": "ptr_project"})
		case key == "POST /api/stacks/create/standalone/string":
			writeJSON(map[string]any{"Id": 11})
		case r.Method == "GET" && sscan(r.URL.Path, "/api/stacks/%d", &id):
			s, ok := f.stacks[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSON(s)
		case r.Method == "GET" && sscan(r.URL.Path, "/api/teams/%d", &id):
			writeJSON(map[string]any{"Name": f.teams[id]})
		case r.Method == "GET" && sscan(r.URL.Path, "/api/users/%d", &id):
			writeJSON(map[string]any{"Username": f.users[id]})
		case r.Method == "DELETE":
			f.deleted = append(f.deleted, r.URL.Path)
			f.deleteAuth = append(f.deleteAuth, r.Header.Get("X-API-Key")+r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s", key)
			http.NotFound(w, r)
		}
	}
}

func sscan(path, format string, id *int) bool {
	n, err := fmt.Sscanf(path, format, id)
	return err == nil && n == 1 && fmt.Sprintf(format, *id) == path
}

func TestProvisionTier_Success(t *testing.T) {
	f := newFakePortainer()
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	api := config.NewPortainerAPI(srv.URL, false)
	api.Token = "ptr_admin"
	cfg := &config.GlobalConfig{PortainerEnvID: 1}

	teamID, userID, token, stackID, err := provisionTier(api, cfg, "app-dev", "services: {}\n")
	require.NoError(t, err)
	assert.Equal(t, 7, teamID)
	assert.Equal(t, 9, userID)
	assert.Equal(t, "ptr_project", token)
	assert.Equal(t, 11, stackID)
	assert.Equal(t, "ptr_admin", api.Token, "admin token must be restored")
	assert.Empty(t, f.deleted)
}

func TestProvisionTier_CleansUpOnFailure(t *testing.T) {
	tests := []struct {
		name        string
		failStep    string
		wantDeleted []string
	}{
		{"endpoint access fails", "PUT /api/endpoints/1", []string{"/api/teams/7"}},
		{"user creation fails", "POST /api/users", []string{"/api/teams/7"}},
		{"membership fails", "POST /api/team_memberships", []string{"/api/users/9", "/api/teams/7"}},
		{"token generation fails", "POST /api/users/9/tokens", []string{"/api/users/9", "/api/teams/7"}},
		{"stack creation fails", "POST /api/stacks/create", []string{"/api/users/9", "/api/teams/7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakePortainer()
			f.failStep = tt.failStep
			srv := httptest.NewServer(f.handler(t))
			defer srv.Close()

			api := config.NewPortainerAPI(srv.URL, false)
			api.Token = "ptr_admin"

			teamID, userID, token, stackID, err := provisionTier(api, &config.GlobalConfig{PortainerEnvID: 1}, "app-dev", "services: {}\n")
			require.Error(t, err)
			assert.Zero(t, teamID)
			assert.Zero(t, userID)
			assert.Empty(t, token)
			assert.Zero(t, stackID)

			assert.Equal(t, tt.wantDeleted, f.deleted)
			for _, auth := range f.deleteAuth {
				assert.Equal(t, "ptr_admin", auth, "cleanup must run as admin")
			}
			assert.Equal(t, "ptr_admin", api.Token)
		})
	}
}

func TestSetEndpointTeamAccess_UsesStandardUserRole(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		}
		_, _ = w.Write([]byte(`{"TeamAccessPolicies":{}}`))
	}))
	defer srv.Close()

	require.NoError(t, config.NewPortainerAPI(srv.URL, false).SetEndpointTeamAccess(1, 5))
	policies := body["TeamAccessPolicies"].(map[string]any)
	assert.Equal(t, float64(config.StandardUserRoleID), policies["5"].(map[string]any)["RoleId"])
}

func TestResolveCompose(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, name := range []string{"ai-stack", "n8n"} {
		t.Run("pack "+name, func(t *testing.T) {
			content, pack, _, err := resolveCompose(name, "p")
			require.NoError(t, err)
			require.NotNil(t, pack)
			assert.Contains(t, content, "services:")
		})
	}

	content, pack, _, err := resolveCompose("api", "p")
	require.NoError(t, err)
	assert.Nil(t, pack)
	assert.Contains(t, content, "services:")

	content, _, _, err = resolveCompose("default", "p")
	require.NoError(t, err)
	assert.Contains(t, content, "services")

	_, _, _, err = resolveCompose("does-not-exist", "p")
	assert.ErrorContains(t, err, "not found")
}

func TestPacksDoNotAddUnpinnedMCPs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"ai-stack", "n8n"} {
		pack, _, err := loadPack(name)
		require.NoError(t, err)
		for mcpName, m := range pack.MCPs {
			for _, arg := range m.Args {
				assert.NotContains(t, arg, "@anthropic/", "pack %s MCP %s", name, mcpName)
			}
		}
	}
}

func TestRenderMCPTemplates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	for _, tc := range []struct {
		name string
		data TemplateData
	}{
		{"dev default", TemplateData{Tier: "dev", StackName: "app-dev", PortainerURL: "https://p:9443", APIToken: "ptr_\"x", MCPBinaryPath: "/bin/towline-mcp", ApprovalWebhook: "https://approve"}},
		{"prod self-signed", TemplateData{Tier: "prod", StackName: "app-prod", PortainerURL: "https://p:9443", APIToken: "ptr_x", MCPBinaryPath: "towline-mcp", SkipTLSVerify: true, ApprovalWebhook: "https://approve"}},
	} {
		for _, tmpl := range []string{"mcp-json.tmpl", "cursor-mcp.tmpl", "gemini-settings.tmpl"} {
			t.Run(tc.name+" "+tmpl, func(t *testing.T) {
				out := filepath.Join(dir, "sub-"+tc.data.Tier, tmpl+".json")
				require.NoError(t, renderTemplate(tmpl, tc.data, out, secretFileMode))

				info, err := os.Stat(out)
				require.NoError(t, err)
				assert.Equal(t, secretFileMode, info.Mode().Perm())
				dirInfo, err := os.Stat(filepath.Dir(out))
				require.NoError(t, err)
				assert.Equal(t, secretDirMode, dirInfo.Mode().Perm())

				raw, err := os.ReadFile(out)
				require.NoError(t, err)
				var cfg struct {
					MCPServers map[string]struct {
						Args []string          `json:"args"`
						Env  map[string]string `json:"env"`
					} `json:"mcpServers"`
				}
				require.NoError(t, json.Unmarshal(raw, &cfg), string(raw))
				server := cfg.MCPServers["towline-"+tc.data.Tier]

				assert.Equal(t, tc.data.APIToken, server.Env["TOWLINE_PORTAINER_TOKEN"])
				assert.NotContains(t, server.Args, "-token")
				assert.Equal(t, tc.data.SkipTLSVerify, contains(server.Args, "-skip-tls-verify"))
				assert.Equal(t, tc.data.Tier == "prod", contains(server.Args, "-approval-webhook"))
			})
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestGitignoreTemplateCoversSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	content, err := renderTemplateToString("gitignore.tmpl", TemplateData{})
	require.NoError(t, err)
	lines := strings.Split(content, "\n")
	for _, entry := range secretGitignoreEntries {
		assert.Contains(t, lines, entry)
	}
}

func TestEnsureGitignore(t *testing.T) {
	dir := t.TempDir()

	// Creates the file when missing
	require.NoError(t, ensureGitignore(dir))
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	for _, entry := range secretGitignoreEntries {
		assert.Contains(t, string(data), entry+"\n")
	}

	// Appends only missing entries to an existing file
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n/.mcp.json"), 0644))
	require.NoError(t, ensureGitignore(dir))
	data, err = os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "node_modules/\n/.mcp.json\n"))
	assert.Equal(t, 1, strings.Count(string(data), ".mcp.json"))
	assert.Contains(t, string(data), "towline.json\n")

	// Idempotent
	require.NoError(t, ensureGitignore(dir))
	again, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, string(data), string(again))
}

func TestValidateProjectName(t *testing.T) {
	for _, ok := range []string{"app", "my-app", "app_2", "0app"} {
		assert.NoError(t, validateProjectName(ok), ok)
	}
	for _, bad := range []string{"", "App", "../etc", "a/b", "a:b", "-app", "a b", strings.Repeat("a", 64)} {
		assert.Error(t, validateProjectName(bad), bad)
	}
}

// setupDestroy writes a global config and a project towline.json pointing at
// the fake Portainer.
func setupDestroy(t *testing.T, portainerURL string, projectCfg *config.ProjectConfig) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	projects := filepath.Join(home, "projects")
	require.NoError(t, config.SaveGlobalConfig(&config.GlobalConfig{
		PortainerURL:    portainerURL,
		PortainerAPIKey: "ptr_admin",
		PortainerEnvID:  1,
		ProjectsDir:     projects,
	}))
	projectDir := filepath.Join(projects, "app")
	require.NoError(t, os.MkdirAll(projectDir, 0755))
	require.NoError(t, config.SaveProjectConfig(projectDir, projectCfg))
}

func TestDestroy_DeletesVerifiedResources(t *testing.T) {
	f := newFakePortainer()
	f.stacks[11] = config.StackInfo{ID: 11, Name: "app-dev", EndpointID: 3}
	f.teams[7] = "team-app-dev"
	f.users[9] = "towline-app-dev"
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	setupDestroy(t, srv.URL, &config.ProjectConfig{StackName: "app-dev", StackID: 11, TeamID: 7, UserID: 9, EnvID: 1})
	require.NoError(t, runDestroy([]string{"--confirm", "app"}))
	assert.Equal(t, []string{"/api/stacks/11", "/api/users/9", "/api/teams/7"}, f.deleted)
}

func TestDestroy_RefusesTamperedIDs(t *testing.T) {
	f := newFakePortainer()
	// IDs in towline.json point at another project's resources.
	f.stacks[21] = config.StackInfo{ID: 21, Name: "victim-dev", EndpointID: 1}
	f.teams[22] = "team-victim-dev"
	f.users[23] = "towline-victim-dev"
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	setupDestroy(t, srv.URL, &config.ProjectConfig{StackName: "app-dev", StackID: 21, TeamID: 22, UserID: 23, EnvID: 1})
	require.NoError(t, runDestroy([]string{"--confirm", "app"}))
	assert.Empty(t, f.deleted)
}

func TestDestroy_RefusesForeignStackName(t *testing.T) {
	f := newFakePortainer()
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	setupDestroy(t, srv.URL, &config.ProjectConfig{StackName: "victim-dev", StackID: 21, TeamID: 22, UserID: 23})
	err := runDestroy([]string{"--confirm", "app"})
	assert.ErrorContains(t, err, "does not belong")
	assert.Empty(t, f.deleted)
}

func TestInit_EndToEndWithPack(t *testing.T) {
	f := newFakePortainer()
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	projects := filepath.Join(home, "projects")
	require.NoError(t, config.SaveGlobalConfig(&config.GlobalConfig{
		PortainerURL:    srv.URL,
		PortainerAPIKey: "ptr_admin",
		PortainerEnvID:  1,
		ProjectsDir:     projects,
	}))
	t.Chdir(t.TempDir()) // not an existing codebase

	require.NoError(t, runInit([]string{"--template", "ai-stack", "app"}))
	dir := filepath.Join(projects, "app")

	for _, p := range []string{".mcp.json", ".claude/settings.json", ".cursor/mcp.json", ".gemini/settings.json", "towline.json"} {
		info, err := os.Stat(filepath.Join(dir, p))
		require.NoError(t, err, p)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), p)
	}

	mcpJSON, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	require.NoError(t, err)
	assert.Contains(t, string(mcpJSON), `"TOWLINE_PORTAINER_TOKEN": "ptr_project"`)
	assert.NotContains(t, string(mcpJSON), "-skip-tls-verify")
	assert.NotContains(t, string(mcpJSON), "-token")

	compose, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(compose), "ollama")

	_, err = os.Stat(filepath.Join(dir, "skills", "ai-ops.md"))
	assert.NoError(t, err)

	// Nothing token-bearing was committed.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		out, err := exec.Command("git", "-C", dir, "ls-files").Output()
		require.NoError(t, err)
		for _, secret := range []string{".mcp.json", "towline.json", ".claude/settings.json"} {
			assert.NotContains(t, strings.Split(string(out), "\n"), secret)
		}
	}
}

func TestInit_UnknownTemplateCreatesNothing(t *testing.T) {
	f := newFakePortainer()
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, config.SaveGlobalConfig(&config.GlobalConfig{
		PortainerURL: srv.URL, PortainerAPIKey: "ptr_admin", PortainerEnvID: 1, ProjectsDir: filepath.Join(home, "projects"),
	}))
	t.Chdir(t.TempDir())

	err := runInit([]string{"--template", "nope", "app"})
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, f.requests, "no Portainer resources should be created")
}
