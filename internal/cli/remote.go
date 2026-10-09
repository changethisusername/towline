package cli

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"

	towline "github.com/changethisusername/towline"
	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/gateway"
	"github.com/changethisusername/towline/pkg/config"
	"gopkg.in/yaml.v3"
)

const (
	remoteStackName  = "towline-gateway"
	defaultImageRepo = "ghcr.io/changethisusername/towline-mcp"
	cloudflaredImage = "cloudflare/cloudflared:2026.9.1"
)

// remoteConfig is the CLI's record of the remote gateway, kept in
// ~/.towline/remote.yaml (0600). The gateway's own config is generated from
// it and from each project's towline.json on every deploy.
type remoteConfig struct {
	// URL is the gateway's public origin (https://mcp.example.com).
	URL string `yaml:"url"`
	// Projects are project directories the gateway serves.
	Projects []string `yaml:"projects"`
	// OwnerTokenHash logs in to the gateway page.
	OwnerTokenHash string `yaml:"owner_token_hash"`
	// TunnelToken runs the Cloudflare Tunnel (empty: no tunnel).
	TunnelToken string `yaml:"tunnel_token,omitempty"`
	// Port publishes the gateway on the Docker host (0: not published).
	Port int `yaml:"port,omitempty"`
	// PortainerURL is Portainer as seen from the gateway container
	// (default: the global portainer_url, with localhost mapped to the host).
	PortainerURL string `yaml:"portainer_url,omitempty"`
	Image        string `yaml:"image,omitempty"`
	// Approval is "always" (changes through the gateway always need the
	// owner's approval) or "prod".
	Approval string `yaml:"approval,omitempty"`
	NtfyURL  string `yaml:"ntfy_url,omitempty"`
	// ComposeDir, when set, is where the gateway's compose file is written
	// instead of deploying through Portainer; later commands reuse it.
	ComposeDir string `yaml:"compose_dir,omitempty"`
}

// tunnelTokenPattern is the shape of a Cloudflare Tunnel token (base64).
var tunnelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{20,}$`)

func remoteConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".towline", "remote.yaml"), nil
}

func loadRemoteConfig() (*remoteConfig, error) {
	path, err := remoteConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no remote gateway configured; run 'towline remote setup' first")
	}
	if err != nil {
		return nil, err
	}
	var rc remoteConfig
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return &rc, nil
}

func saveRemoteConfig(rc *remoteConfig) error {
	path, err := remoteConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), secretDirMode); err != nil {
		return err
	}
	data, err := yaml.Marshal(rc)
	if err != nil {
		return err
	}
	return writeFileMode(path, data, secretFileMode)
}

func runRemote(args []string) error {
	usage := func() {
		fmt.Println("Usage: towline remote <setup|deploy|add|remove|status|owner-token>")
		fmt.Println()
		fmt.Println("Run a remote MCP gateway so apps like Claude or ChatGPT/Codex can use your")
		fmt.Println("projects as a connector, through a Cloudflare Tunnel (works behind NAT).")
		fmt.Println()
		fmt.Println("  setup         Configure the gateway (URL, tunnel, projects) and deploy it")
		fmt.Println("  deploy        Deploy or update the gateway (after rotating keys, editing compose policy, ...)")
		fmt.Println("  add <name>    Serve more projects")
		fmt.Println("  remove <name> Stop serving projects (their connections lose access)")
		fmt.Println("  status        Show the configuration and check the gateway is up")
		fmt.Println("  owner-token   Issue a new owner token for the gateway page")
	}
	if len(args) < 1 {
		usage()
		return fmt.Errorf("missing remote command")
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		usage()
		return nil
	case "setup":
		return runRemoteSetup(args[1:])
	case "deploy":
		return runRemoteDeploy(args[1:])
	case "add":
		return runRemoteAddRemove(args[1:], true)
	case "remove":
		return runRemoteAddRemove(args[1:], false)
	case "status":
		return runRemoteStatus(args[1:])
	case "owner-token":
		return runRemoteOwnerToken(args[1:])
	default:
		return fmt.Errorf("unknown remote command: %s", args[0])
	}
}

func runRemoteSetup(args []string) error {
	fs := flag.NewFlagSet("remote setup", flag.ContinueOnError)
	publicURL := fs.String("url", "", "Public URL of the gateway, e.g. https://mcp.example.com (required)")
	tunnelToken := fs.String("tunnel-token", "", "Cloudflare Tunnel token (or set TUNNEL_TOKEN)")
	noTunnel := fs.Bool("no-tunnel", false, "Don't run a Cloudflare Tunnel (bring your own reverse proxy; use with --port)")
	port := fs.Int("port", 0, "Publish the gateway on this host port (only for your own reverse proxy)")
	all := fs.Bool("all", false, "Serve every project in the projects directory")
	portainerURL := fs.String("portainer-url", "", "Portainer URL as seen from the gateway container (default: derived from your config)")
	image := fs.String("image", "", "Gateway image (default: "+defaultImageRepo+" at this CLI's version)")
	approvalPolicy := fs.String("approval", "", "Changes made through the gateway need your approval: always (default), or prod (only prod projects)")
	ntfy := fs.String("ntfy-url", "", "Send approval notifications to this ntfy topic URL")
	noDeploy := fs.Bool("no-deploy", false, "Only save the configuration")
	composeDir := fs.String("compose-dir", "", "Write docker-compose.yml and .env here instead of deploying through Portainer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	names, err := positionalArgs(fs)
	if err != nil {
		return err
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	rc := &remoteConfig{}
	if existing, err := loadRemoteConfig(); err == nil {
		rc = existing
	}
	if *publicURL != "" {
		rc.URL = strings.TrimRight(*publicURL, "/")
	}
	if rc.URL == "" {
		return fmt.Errorf("--url is required (the public https address the tunnel serves, e.g. https://mcp.example.com)")
	}
	token := *tunnelToken
	if token == "" {
		token = os.Getenv("TUNNEL_TOKEN")
	}
	switch {
	case *noTunnel:
		rc.TunnelToken = ""
	case token != "":
		t := strings.TrimSpace(token)
		if !tunnelTokenPattern.MatchString(t) {
			return fmt.Errorf("the tunnel token doesn't look like a Cloudflare Tunnel token (copy the long string after --token in the dashboard's install command)")
		}
		rc.TunnelToken = t
	case rc.TunnelToken == "":
		return fmt.Errorf("a Cloudflare Tunnel token is required (--tunnel-token or TUNNEL_TOKEN), or pass --no-tunnel with --port to use your own reverse proxy")
	}
	if *port != 0 {
		if *port < 1 || *port > 65535 {
			return fmt.Errorf("invalid --port")
		}
		rc.Port = *port
	}
	if *noTunnel && rc.Port == 0 {
		return fmt.Errorf("without a tunnel, publish the gateway with --port and put your reverse proxy in front of it")
	}
	if *portainerURL != "" {
		rc.PortainerURL = *portainerURL
	}
	if *image != "" {
		rc.Image = *image
	}
	switch *approvalPolicy {
	case gateway.ApprovalAlways, gateway.ApprovalProd:
		rc.Approval = *approvalPolicy
	case "":
		// Re-running setup keeps the earlier choice.
		if rc.Approval == "" {
			rc.Approval = gateway.ApprovalAlways
		}
	default:
		return fmt.Errorf("--approval must be always or prod")
	}
	if *ntfy != "" {
		rc.NtfyURL = *ntfy
	}

	if *composeDir != "" {
		abs, err := filepath.Abs(*composeDir)
		if err != nil {
			return err
		}
		rc.ComposeDir = abs
	}

	ownerToken := ""
	if rc.OwnerTokenHash == "" {
		ownerToken = approval.NewToken()
		rc.OwnerTokenHash = approval.HashToken(ownerToken)
	}

	dirs, err := projectDirs(cfg, names, *all)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if slices.Contains(rc.Projects, d) {
			continue
		}
		if *all {
			// With --all, a project that can't be served is skipped
			// rather than failing the whole setup.
			probe := *rc
			probe.Projects = []string{d}
			if _, err := buildGatewayConfig(cfg, &probe); err != nil {
				fmt.Printf("Skipping %s: %v\n", filepath.Base(d), err)
				continue
			}
		}
		rc.Projects = append(rc.Projects, d)
	}
	if len(rc.Projects) == 0 {
		return fmt.Errorf("name the projects to serve, or pass --all")
	}

	// Check the result builds a valid gateway config before saving.
	gc, err := buildGatewayConfig(cfg, rc)
	if err != nil {
		return err
	}
	rc.URL = gc.PublicURL // canonical: lowercase host, no default port
	if err := saveRemoteConfig(rc); err != nil {
		return err
	}
	fmt.Println("Saved ~/.towline/remote.yaml")
	if ownerToken != "" {
		fmt.Println()
		fmt.Println("Owner token for the gateway page (shown once; keep it in your password manager):")
		fmt.Println()
		fmt.Println("  " + ownerToken)
		fmt.Println()
	}
	if *noDeploy {
		fmt.Println("Run 'towline remote deploy' when you're ready.")
		return nil
	}
	return deployRemote(cfg, rc, rc.ComposeDir)
}

func runRemoteDeploy(args []string) error {
	fs := flag.NewFlagSet("remote deploy", flag.ContinueOnError)
	composeDir := fs.String("compose-dir", "", "Write docker-compose.yml and .env here instead of deploying through Portainer (remembered)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	rc, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	if *composeDir != "" {
		abs, err := filepath.Abs(*composeDir)
		if err != nil {
			return err
		}
		rc.ComposeDir = abs
		if err := saveRemoteConfig(rc); err != nil {
			return err
		}
	}
	return deployRemote(cfg, rc, rc.ComposeDir)
}

func runRemoteAddRemove(args []string, add bool) error {
	fs := flag.NewFlagSet("remote add", flag.ContinueOnError)
	noDeploy := fs.Bool("no-deploy", false, "Only save the configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	names, err := positionalArgs(fs)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("name at least one project")
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	rc, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	if add {
		dirs, err := projectDirs(cfg, names, false)
		if err != nil {
			return err
		}
		for _, d := range dirs {
			if !slices.Contains(rc.Projects, d) {
				rc.Projects = append(rc.Projects, d)
			}
		}
	} else {
		// Match by directory name without reading towline.json, so a
		// project whose directory is already gone can still be removed.
		for _, name := range names {
			before := len(rc.Projects)
			rc.Projects = slices.DeleteFunc(rc.Projects, func(p string) bool { return filepath.Base(p) == name })
			if len(rc.Projects) == before {
				return fmt.Errorf("%s is not served by the gateway", name)
			}
		}
	}
	if len(rc.Projects) == 0 {
		return fmt.Errorf("the gateway needs at least one project")
	}
	if _, err := buildGatewayConfig(cfg, rc); err != nil {
		return err
	}
	if err := saveRemoteConfig(rc); err != nil {
		return err
	}
	if *noDeploy {
		return nil
	}
	return deployRemote(cfg, rc, rc.ComposeDir)
}

func runRemoteOwnerToken(args []string) error {
	fs := flag.NewFlagSet("remote owner-token", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	rc, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	token := approval.NewToken()
	oldHash := rc.OwnerTokenHash
	rc.OwnerTokenHash = approval.HashToken(token)
	// Deploy first: if it fails, the old token keeps working and the new
	// one is never shown.
	if err := deployRemote(cfg, rc, rc.ComposeDir); err != nil {
		rc.OwnerTokenHash = oldHash
		return fmt.Errorf("owner token not changed: %w", err)
	}
	if err := saveRemoteConfig(rc); err != nil {
		return fmt.Errorf("the gateway now uses a new owner token, but saving ~/.towline/remote.yaml failed (run 'towline remote owner-token' again): %w", err)
	}
	fmt.Println()
	fmt.Println("New owner token (shown once; the old one no longer works):")
	fmt.Println()
	fmt.Println("  " + token)
	fmt.Println()
	return nil
}

// dropFromRemote removes a destroyed project from the gateway's
// configuration, if one exists, and says how to apply it.
func dropFromRemote(projectDir string) {
	rc, err := loadRemoteConfig()
	if err != nil {
		return
	}
	dir := resolvePath(projectDir)
	n := len(rc.Projects)
	rc.Projects = slices.DeleteFunc(rc.Projects, func(p string) bool { return p == dir || p == projectDir })
	if len(rc.Projects) == n {
		return
	}
	if len(rc.Projects) == 0 {
		if err := saveRemoteConfig(rc); err != nil {
			fmt.Printf("Warning: failed to update the remote gateway config: %v\n", err)
			return
		}
		where := "delete the " + remoteStackName + " stack in Portainer"
		if rc.ComposeDir != "" {
			where = "run 'docker compose down' in " + rc.ComposeDir
		}
		fmt.Printf("The remote gateway now serves no projects; %s to stop it.\n", where)
		return
	}
	if err := saveRemoteConfig(rc); err != nil {
		fmt.Printf("Warning: failed to remove the project from the remote gateway config: %v\n", err)
		return
	}
	fmt.Println("Removed from the remote gateway config; run 'towline remote deploy' so the gateway stops serving it.")
}

func runRemoteStatus(args []string) error {
	fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	rc, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	fmt.Printf("Gateway:   %s\n", rc.URL)
	fmt.Printf("Page:      %s/ui\n", rc.URL)
	fmt.Printf("MCP URL:   %s/mcp\n", rc.URL)
	tunnel := "Cloudflare Tunnel"
	if rc.TunnelToken == "" {
		tunnel = fmt.Sprintf("none (host port %d)", rc.Port)
	}
	fmt.Printf("Tunnel:    %s\n", tunnel)
	fmt.Printf("Approval:  %s\n", rc.Approval)
	if gc, err := buildGatewayConfig(cfg, rc); err != nil {
		fmt.Printf("Projects:  error: %v\n", err)
	} else {
		for _, p := range gc.Projects {
			fmt.Printf("Project:   %s (%s)\n", p.Name, p.Tier)
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rc.URL + "/healthz")
	if err != nil {
		fmt.Printf("Health:    unreachable (%v)\n", err)
		return nil
	}
	resp.Body.Close()
	fmt.Printf("Health:    HTTP %d\n", resp.StatusCode)
	return nil
}

// projectDirs resolves project names (in the projects directory) or all
// projects to directories that hold a towline.json.
func projectDirs(cfg *config.GlobalConfig, names []string, all bool) ([]string, error) {
	var dirs []string
	if all {
		entries, err := os.ReadDir(cfg.ProjectsDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read projects directory: %w", err)
		}
		for _, e := range entries {
			names = append(names, e.Name())
		}
	}
	for _, name := range names {
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, fmt.Errorf("invalid project name %q", name)
		}
		dir := filepath.Join(cfg.ProjectsDir, name)
		if _, err := os.Stat(filepath.Join(dir, "towline.json")); err != nil {
			if all {
				continue
			}
			return nil, fmt.Errorf("%s is not a towline project (no towline.json)", dir)
		}
		dirs = append(dirs, resolvePath(dir))
	}
	return dirs, nil
}

var stackTierSuffix = regexp.MustCompile(`-(dev|prod)$`)

// buildGatewayConfig assembles the gateway's config from the remote config
// and each project's towline.json. towline.json is agent-writable, so the
// project must belong to its directory and the tier must agree with the
// stack name; its token can only reach what Portainer already lets that
// project's team reach.
func buildGatewayConfig(cfg *config.GlobalConfig, rc *remoteConfig) (*gateway.Config, error) {
	portainerURL, _ := gatewayPortainerURL(cfg, rc)
	skip, _ := cfg.InsecureTLS()
	gc := &gateway.Config{
		PublicURL:       rc.URL,
		OwnerTokenHash:  rc.OwnerTokenHash,
		TrustCloudflare: rc.TunnelToken != "" && rc.Port == 0,
		NtfyURL:         rc.NtfyURL,
		Portainer:       gateway.PortainerConfig{URL: portainerURL, SkipTLSVerify: skip},
	}
	for _, dir := range rc.Projects {
		pc, err := config.LoadProjectConfig(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		if err := checkProjectDirAnchor(cfg, dir, pc); err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		if pc.MCPArgs.Token == "" || pc.StackName == "" {
			return nil, fmt.Errorf("%s: towline.json is missing the token or stack name", dir)
		}
		tier := pc.Tier
		if m := stackTierSuffix.FindStringSubmatch(pc.StackName); m != nil && m[1] != tier {
			return nil, fmt.Errorf("%s: stack %q does not match tier %q in towline.json", dir, pc.StackName, tier)
		}
		if !gateway.ValidProjectName(pc.StackName) {
			return nil, fmt.Errorf("%s: stack name %q can't be served remotely (remote tool names need 1-24 lowercase letters, digits and dashes)", dir, pc.StackName)
		}
		p := gateway.ProjectConfig{
			Name:     pc.StackName,
			Stack:    pc.StackName,
			Tier:     tier,
			Token:    pc.MCPArgs.Token,
			Approval: rc.Approval,
		}
		if cp := pc.ComposePolicy; cp != nil {
			p.ComposePolicy = gateway.ComposePolicyConfig{
				Mode:            cp.Mode,
				AllowBindMounts: cp.AllowBindMounts,
				AllowNetworks:   cp.AllowNetworks,
				AllowVolumes:    cp.AllowVolumes,
			}
		}
		gc.Projects = append(gc.Projects, p)
	}
	if err := gc.Validate(); err != nil {
		return nil, err
	}
	return gc, nil
}

// gatewayPortainerURL is Portainer's address from inside the gateway
// container. localhost there is the container itself, so a local Portainer
// is reached through the Docker host instead.
func gatewayPortainerURL(cfg *config.GlobalConfig, rc *remoteConfig) (string, bool) {
	raw := rc.PortainerURL
	if raw == "" {
		raw = cfg.PortainerURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		port := u.Port()
		u.Host = "host.docker.internal"
		if port != "" {
			u.Host += ":" + port
		}
		return u.String(), true
	case "host.docker.internal":
		return u.String(), true
	}
	return u.String(), false
}

func gatewayImage(rc *remoteConfig) string {
	if rc.Image != "" {
		return rc.Image
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(Version) {
		return defaultImageRepo + ":" + strings.TrimPrefix(Version, "v")
	}
	return defaultImageRepo + ":latest"
}

// renderGatewayCompose renders the gateway stack's compose file.
func renderGatewayCompose(cfg *config.GlobalConfig, rc *remoteConfig) (string, error) {
	raw, err := towline.EmbeddedTemplates.ReadFile("templates/remote/docker-compose.yml.tmpl")
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("compose").Parse(string(raw))
	if err != nil {
		return "", err
	}
	_, hostGateway := gatewayPortainerURL(cfg, rc)
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"Image":            gatewayImage(rc),
		"CloudflaredImage": cloudflaredImage,
		"Tunnel":           rc.TunnelToken != "",
		"Port":             rc.Port,
		"HostGateway":      hostGateway,
	})
	return buf.String(), err
}

// gatewayEnv is the stack's environment: the gateway config (which holds
// the project tokens) and the tunnel token.
func gatewayEnv(gc *gateway.Config, rc *remoteConfig) ([]config.StackEnvVar, error) {
	data, err := yaml.Marshal(gc)
	if err != nil {
		return nil, err
	}
	env := []config.StackEnvVar{{Name: gateway.ConfigEnvVar, Value: "base64:" + base64.StdEncoding.EncodeToString(data)}}
	if rc.TunnelToken != "" {
		env = append(env, config.StackEnvVar{Name: "TUNNEL_TOKEN", Value: rc.TunnelToken})
	}
	return env, nil
}

func deployRemote(cfg *config.GlobalConfig, rc *remoteConfig, composeDir string) error {
	gc, err := buildGatewayConfig(cfg, rc)
	if err != nil {
		return err
	}
	if _, hostGateway := gatewayPortainerURL(cfg, rc); hostGateway && !gc.Portainer.SkipTLSVerify && strings.HasPrefix(gc.Portainer.URL, "https://") {
		fmt.Println("Note: the gateway reaches Portainer at " + gc.Portainer.URL + "; its TLS certificate must be valid for that name. Pass --portainer-url to use another address.")
	}
	for _, p := range gc.Projects {
		if p.ComposePolicy.Mode == "off" {
			fmt.Printf("Warning: project %s has its compose security policy turned off; remote apps can deploy privileged containers there.\n", p.Name)
		}
	}
	compose, err := renderGatewayCompose(cfg, rc)
	if err != nil {
		return err
	}
	env, err := gatewayEnv(gc, rc)
	if err != nil {
		return err
	}

	if composeDir != "" {
		if err := os.MkdirAll(composeDir, secretDirMode); err != nil {
			return err
		}
		if err := writeFileMode(filepath.Join(composeDir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
			return err
		}
		var dotenv strings.Builder
		for _, e := range env {
			fmt.Fprintf(&dotenv, "%s=%s\n", e.Name, e.Value)
		}
		if err := writeFileMode(filepath.Join(composeDir, ".env"), []byte(dotenv.String()), secretFileMode); err != nil {
			return err
		}
		fmt.Printf("Wrote %s/docker-compose.yml and .env (the .env holds secrets; keep it private).\n", composeDir)
		fmt.Printf("Start it with: cd %s && docker compose up -d\n", composeDir)
		printRemoteNext(rc)
		return nil
	}

	api := newAdminAPI(cfg)
	existing, err := api.FindStackByName(cfg.PortainerEnvID, remoteStackName)
	if err != nil {
		return err
	}
	if existing == nil {
		fmt.Printf("Creating stack %s in Portainer...\n", remoteStackName)
		if _, err := api.CreateLocalStackWithEnv(cfg.PortainerEnvID, remoteStackName, compose, env); err != nil {
			return err
		}
	} else {
		fmt.Printf("Updating stack %s in Portainer...\n", remoteStackName)
		if err := api.UpdateLocalStackWithEnv(existing.ID, existing.EndpointID, compose, env); err != nil {
			return err
		}
	}
	fmt.Println("Gateway deployed.")
	printRemoteNext(rc)
	return nil
}

func printRemoteNext(rc *remoteConfig) {
	fmt.Println()
	if rc.TunnelToken != "" {
		u, _ := url.Parse(rc.URL)
		fmt.Println("In the Cloudflare dashboard, give the tunnel this public hostname:")
		fmt.Printf("  %s  ->  http://gateway:8080\n", u.Host)
		fmt.Println()
	}
	fmt.Printf("Then open %s/ui, sign in with your owner token and create a connection.\n", rc.URL)
	fmt.Printf("Add %s/mcp as a connector in Claude, ChatGPT/Codex or any MCP client.\n", rc.URL)
}
