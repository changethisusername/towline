package cli

import (
	"bufio"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/changethisusername/towline/internal/gateway"
	"github.com/changethisusername/towline/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	ferr := fn()
	w.Close()
	os.Stdout = old
	return <-done, ferr
}

// deployedGatewayConfig reads the gateway config from a compose-dir .env.
func deployedGatewayConfig(t *testing.T, dir string) *gateway.Config {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, ".env"))
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), gateway.ConfigEnvVar+"="); ok {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "base64:"))
			require.NoError(t, err)
			gc, err := gateway.ParseConfig(raw)
			require.NoError(t, err, "the deployed config must load in the gateway")
			return gc
		}
	}
	t.Fatal("no gateway config in .env")
	return nil
}

func autoDeployOf(gc *gateway.Config, name string) *gateway.AutoDeployConfig {
	p, ok := gc.Project(name)
	if !ok {
		return nil
	}
	return p.AutoDeploy
}

func setAutoDeploy(t *testing.T, projectsDir, name string, ad map[string]any) {
	t.Helper()
	path := filepath.Join(projectsDir, name, "towline.json")
	pc, err := config.LoadProjectConfig(filepath.Dir(path))
	require.NoError(t, err)
	pc.AutoDeploy = nil
	if ad != nil {
		pc.AutoDeploy = &config.AutoDeployConfig{}
		pc.AutoDeploy.Repo, _ = ad["repo"].(string)
		pc.AutoDeploy.Branch, _ = ad["branch"].(string)
		pc.AutoDeploy.ComposePath, _ = ad["compose_path"].(string)
		pc.AutoDeploy.CommitEnv, _ = ad["commit_env"].(string)
		if v, ok := ad["require_checks"].(bool); ok {
			pc.AutoDeploy.RequireChecks = &v
		}
	}
	require.NoError(t, config.SaveProjectConfig(filepath.Dir(path), pc))
}

func TestRemoteAutoDeployNeedsOwnerAcceptance(t *testing.T) {
	cfg := remoteTestEnv(t, "http://127.0.0.1:1")
	out := filepath.Join(t.TempDir(), "gw")
	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "eyJhIjoidGVzdCJ9tunneltoken00", "--compose-dir", out, "--approval", "prod", "shop", "blog"}))

	// An agent adds an auto_deploy block: deploying leaves it off.
	setAutoDeploy(t, cfg.ProjectsDir, "blog", map[string]any{"repo": "acme/blog"})
	stdout, err := captureStdout(t, func() error { return runRemote([]string{"deploy"}) })
	require.NoError(t, err)
	assert.Nil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))
	assert.Contains(t, stdout, "--accept-auto-deploy")
	assert.Contains(t, stdout, "acme/blog@main:docker-compose.yml")

	// A bare --accept-auto-deploy names nothing: it lists what is pending,
	// old → new, and deploys nothing.
	err = runRemote([]string{"deploy", "--accept-auto-deploy"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blog-dev: (none accepted)\n    → repo=acme/blog branch=main")
	err = runRemote([]string{"deploy", "--accept-auto-deploy", "--compose-dir", out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blog-dev")
	rc, err := loadRemoteConfig()
	require.NoError(t, err)
	assert.Empty(t, rc.AutoDeployAccepted)
	// Unknown names, or projects without a block, are refused.
	assert.ErrorContains(t, runRemote([]string{"deploy", "--accept-auto-deploy", "nope"}), "nope")
	assert.ErrorContains(t, runRemote([]string{"deploy", "--accept-auto-deploy", "shop"}), "shop")

	// The owner accepts it (by directory or stack name).
	stdout, err = captureStdout(t, func() error { return runRemote([]string{"deploy", "--accept-auto-deploy", "blog"}) })
	require.NoError(t, err)
	ad := autoDeployOf(deployedGatewayConfig(t, out), "blog-dev")
	require.NotNil(t, ad)
	assert.Equal(t, "acme/blog", ad.Repo)
	assert.Contains(t, stdout, "Auto-deploy: blog-dev deploys acme/blog@main:docker-compose.yml")
	assert.Contains(t, stdout, "WITHOUT your approval", "approval prod on a dev project")
	assert.Contains(t, stdout, "public repositories only")
	rc, err = loadRemoteConfig()
	require.NoError(t, err)
	require.Len(t, rc.AutoDeployAccepted, 1)

	// Later deploys (and other commands) keep it.
	require.NoError(t, runRemote([]string{"deploy"}))
	assert.NotNil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))
	require.NoError(t, runRemote([]string{"owner-token"}))
	assert.NotNil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))

	// Any change (another repo, checks off) needs acceptance again.
	for _, changed := range []map[string]any{
		{"repo": "evil/blog"},
		{"repo": "acme/blog", "require_checks": false},
		{"repo": "acme/blog", "branch": "dev"},
		{"repo": "acme/blog", "compose_path": "other.yml"},
		{"repo": "acme/blog", "commit_env": "GIT_SHA"},
	} {
		setAutoDeploy(t, cfg.ProjectsDir, "blog", changed)
		require.NoError(t, runRemote([]string{"deploy"}))
		assert.Nil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"), "%v", changed)
	}
	// Writing the accepted block back (defaults spelled out) is still accepted.
	setAutoDeploy(t, cfg.ProjectsDir, "blog", map[string]any{"repo": "acme/blog", "branch": "main", "require_checks": true})
	require.NoError(t, runRemote([]string{"deploy"}))
	assert.NotNil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))

	// A compose policy change turns it off too, until accepted again.
	setComposePolicy(t, cfg.ProjectsDir, "blog", &config.ComposePolicyConfig{Mode: "enforce", AllowBindMounts: []string{"/"}})
	stdout, err = captureStdout(t, func() error { return runRemote([]string{"deploy"}) })
	require.NoError(t, err)
	assert.Nil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))
	assert.Contains(t, stdout, "compose policy")
	err = runRemote([]string{"deploy", "--accept-auto-deploy"})
	assert.ErrorContains(t, err, "bind_mounts=[/]")
	require.NoError(t, runRemote([]string{"deploy", "--accept-auto-deploy=blog-dev"}))
	assert.NotNil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))

	// An invalid block is ignored with a notice, and doesn't break other
	// commands; it fails only when accepting it, naming the directory.
	setAutoDeploy(t, cfg.ProjectsDir, "blog", map[string]any{"repo": "acme/blog", "compose_path": "../../etc/x.yml"})
	stdout, err = captureStdout(t, func() error { return runRemote([]string{"deploy"}) })
	require.NoError(t, err)
	assert.Contains(t, stdout, "invalid, ignored")
	assert.Nil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"))
	require.NoError(t, runRemote([]string{"owner-token"}))
	_, err = captureStdout(t, func() error { return runRemote([]string{"status"}) })
	require.NoError(t, err)
	err = runRemote([]string{"deploy", "--accept-auto-deploy", "blog"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join(cfg.ProjectsDir, "blog"))
}

// Accepting one project leaves another project's new block off.
func TestRemoteAutoDeployAcceptsOnlyNamedProjects(t *testing.T) {
	cfg := remoteTestEnv(t, "http://127.0.0.1:1")
	out := filepath.Join(t.TempDir(), "gw")
	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "eyJhIjoidGVzdCJ9tunneltoken00", "--compose-dir", out, "shop", "blog"}))
	setAutoDeploy(t, cfg.ProjectsDir, "blog", map[string]any{"repo": "acme/blog"})
	setAutoDeploy(t, cfg.ProjectsDir, "shop", map[string]any{"repo": "evil/shop"})
	stdout, err := captureStdout(t, func() error { return runRemote([]string{"deploy", "--accept-auto-deploy", "blog"}) })
	require.NoError(t, err)
	gc := deployedGatewayConfig(t, out)
	assert.NotNil(t, autoDeployOf(gc, "blog-dev"))
	assert.Nil(t, autoDeployOf(gc, "shop-prod"))
	assert.Contains(t, stdout, "auto-deploy for shop-prod")
	require.NoError(t, runRemote([]string{"deploy", "--accept-auto-deploy", "blog,shop-prod"}))
	assert.NotNil(t, autoDeployOf(deployedGatewayConfig(t, out), "shop-prod"))
}

func TestRemoteGitHubTokenPromptOnTTY(t *testing.T) {
	remoteTestEnv(t, "http://127.0.0.1:1")
	out := filepath.Join(t.TempDir(), "gw")
	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "eyJhIjoidGVzdCJ9tunneltoken00", "--compose-dir", out, "shop"}))
	var prompt strings.Builder
	readStdin = strings.NewReader("ghp_abc\n")
	oldTTY, oldOut := stdinIsTerminal, promptOut
	t.Cleanup(func() { readStdin, stdinIsTerminal, promptOut = os.Stdin, oldTTY, oldOut })
	promptOut = &prompt
	stdinIsTerminal = func() bool { return true }
	require.NoError(t, runRemote([]string{"github", "--token-stdin", "--no-deploy"}))
	assert.Equal(t, "Paste the token, then press Ctrl-D\n", prompt.String())

	prompt.Reset()
	readStdin = strings.NewReader("ghp_def\n")
	stdinIsTerminal = func() bool { return false }
	require.NoError(t, runRemote([]string{"github", "--token-stdin", "--no-deploy"}))
	assert.Empty(t, prompt.String(), "no prompt when the token is piped in")
}

func setComposePolicy(t *testing.T, projectsDir, name string, cp *config.ComposePolicyConfig) {
	t.Helper()
	dir := filepath.Join(projectsDir, name)
	pc, err := config.LoadProjectConfig(dir)
	require.NoError(t, err)
	pc.ComposePolicy = cp
	require.NoError(t, config.SaveProjectConfig(dir, pc))
}

func TestRemoteGitHubSettings(t *testing.T) {
	cfg := remoteTestEnv(t, "http://127.0.0.1:1")
	out := filepath.Join(t.TempDir(), "gw")
	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "eyJhIjoidGVzdCJ9tunneltoken00", "--compose-dir", out, "shop"}))
	setAutoDeploy(t, cfg.ProjectsDir, "shop", map[string]any{"repo": "acme/shop"})
	require.NoError(t, runRemote([]string{"deploy", "--accept-auto-deploy", "shop"}))

	const token = "github_pat_11SECRETVALUE_abc"
	readStdin = strings.NewReader(token + "\n")
	t.Cleanup(func() { readStdin = os.Stdin })
	stdout, err := captureStdout(t, func() error { return runRemote([]string{"github", "--token-stdin", "--webhook"}) })
	require.NoError(t, err)
	assert.NotContains(t, stdout, token)
	assert.Contains(t, stdout, "https://mcp.example.com/hooks/github")
	gc := deployedGatewayConfig(t, out)
	assert.Equal(t, token, gc.GitHub.Token)
	require.Len(t, gc.GitHub.WebhookSecret, 64)
	assert.Contains(t, stdout, gc.GitHub.WebhookSecret, "the secret is shown once")

	// Status never prints either value.
	stdout, err = captureStdout(t, func() error { return runRemote([]string{"status"}) })
	require.NoError(t, err)
	assert.NotContains(t, stdout, token)
	assert.NotContains(t, stdout, gc.GitHub.WebhookSecret)
	assert.Contains(t, stdout, "token set, webhook on")
	assert.Contains(t, stdout, "auto-deploy acme/shop@main:docker-compose.yml")

	// A malformed token is refused without being echoed.
	readStdin = strings.NewReader("ghp_bad token\nX: y")
	err = runRemote([]string{"github", "--token-stdin"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "ghp_bad")
	rc, _ := loadRemoteConfig()
	assert.Equal(t, token, rc.GitHubToken, "a refused token is not saved")

	// The token never comes from argv.
	assert.Error(t, runRemote([]string{"github", "ghp_x"}))

	require.NoError(t, runRemote([]string{"github", "--clear-token", "--clear-webhook"}))
	gc = deployedGatewayConfig(t, out)
	assert.Empty(t, gc.GitHub.Token)
	assert.Empty(t, gc.GitHub.WebhookSecret)
}

func TestRemoteAutoDeployAcceptanceDroppedWithProject(t *testing.T) {
	cfg := remoteTestEnv(t, "http://127.0.0.1:1")
	out := filepath.Join(t.TempDir(), "gw")
	require.NoError(t, runRemote([]string{"setup", "--url", "https://mcp.example.com", "--tunnel-token", "eyJhIjoidGVzdCJ9tunneltoken00", "--compose-dir", out, "shop", "blog"}))
	setAutoDeploy(t, cfg.ProjectsDir, "blog", map[string]any{"repo": "acme/blog"})
	require.NoError(t, runRemote([]string{"deploy", "--accept-auto-deploy", "blog"}))
	require.NoError(t, runRemote([]string{"remove", "blog"}))
	rc, err := loadRemoteConfig()
	require.NoError(t, err)
	assert.Empty(t, rc.AutoDeployAccepted)
	require.NoError(t, runRemote([]string{"add", "blog"}))
	assert.Nil(t, autoDeployOf(deployedGatewayConfig(t, out), "blog-dev"), "re-added project needs a new acceptance")
}
