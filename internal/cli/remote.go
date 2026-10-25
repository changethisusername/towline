package cli

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
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
	// GitHubToken is a read-only GitHub token for auto-deploy (private
	// repositories, higher rate limits).
	GitHubToken string `yaml:"github_token,omitempty"`
	// GitHubWebhookSecret enables the gateway's GitHub webhook.
	GitHubWebhookSecret string `yaml:"github_webhook_secret,omitempty"`
	// AutoDeployAccepted maps a project directory to the fingerprint of
	// the auto_deploy block the owner accepted. towline.json is
	// agent-writable, so a new or changed block stays off until the owner
	// runs 'towline remote deploy --accept-auto-deploy <project>'. The
	// fingerprint covers the whole block and the compose policy.
	AutoDeployAccepted map[string]string `yaml:"auto_deploy_accepted,omitempty"`
}

// readStdin is where 'towline remote github --token-stdin' reads from.
var readStdin io.Reader = os.Stdin

// stdinIsTerminal reports whether readStdin is an interactive terminal,
// and promptOut is where the paste prompt goes (tests replace both).
var (
	stdinIsTerminal = func() bool {
		f, ok := readStdin.(*os.File)
		if !ok {
			return false
		}
		info, err := f.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	promptOut io.Writer = os.Stderr
)

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
	// An acceptance belongs to a served project: a project removed (or
	// destroyed and recreated in the same directory) must be accepted again.
	for dir := range rc.AutoDeployAccepted {
		if !slices.Contains(rc.Projects, dir) {
			delete(rc.AutoDeployAccepted, dir)
		}
	}
	data, err := yaml.Marshal(rc)
	if err != nil {
		return err
	}
	return writeFileMode(path, data, secretFileMode)
}

func runRemote(args []string) error {
	usage := func() {
		fmt.Println("Usage: towline remote <setup|deploy|add|remove|status|owner-token|github>")
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
		fmt.Println("  github        Set the GitHub token and webhook used by auto-deploy")
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
	case "github":
		return runRemoteGitHub(args[1:])
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
			if _, _, err := buildGatewayConfig(cfg, &probe, nil); err != nil {
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
	gc, _, err := buildGatewayConfig(cfg, rc, nil)
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
	return deployRemote(cfg, rc, rc.ComposeDir, nil)
}

func runRemoteDeploy(args []string) error {
	fs := flag.NewFlagSet("remote deploy", flag.ContinueOnError)
	composeDir := fs.String("compose-dir", "", "Write docker-compose.yml and .env here instead of deploying through Portainer (remembered)")
	acceptList := fs.String("accept-auto-deploy", "", "Accept the auto_deploy blocks of these projects (comma-separated) as they are now")
	// A bare --accept-auto-deploy (last, or followed by another flag)
	// names no projects: list what is pending instead of guessing.
	acceptGiven := false
	for i, a := range args {
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "accept-auto-deploy" {
			continue
		}
		acceptGiven = true
		if !hasValue && (i+1 == len(args) || strings.HasPrefix(args[i+1], "-")) {
			args = slices.Concat(args[:i], []string{"--accept-auto-deploy="}, args[i+1:])
		}
		break
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (name projects with --accept-auto-deploy a,b)", fs.Arg(0))
	}
	var accept []string
	for _, n := range strings.Split(*acceptList, ",") {
		if n = strings.TrimSpace(n); n != "" {
			accept = append(accept, n)
		}
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	rc, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	if acceptGiven && len(accept) == 0 {
		return pendingAcceptanceError(cfg, rc)
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
	if err := deployRemote(cfg, rc, rc.ComposeDir, accept); err != nil {
		return err
	}
	if len(accept) > 0 {
		// The gateway already runs with the accepted blocks; if this save
		// fails, the next deploy turns them off again (fails safe).
		if err := saveRemoteConfig(rc); err != nil {
			return fmt.Errorf("failed to record the accepted auto-deploy settings: %w", err)
		}
	}
	return nil
}

// pendingAcceptanceError lists the auto_deploy blocks waiting for the
// owner, for a --accept-auto-deploy that named no projects.
func pendingAcceptanceError(cfg *config.GlobalConfig, rc *remoteConfig) error {
	_, notices, err := buildGatewayConfig(cfg, rc, nil)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("--accept-auto-deploy needs the projects to accept: --accept-auto-deploy <project>[,<project>...]")
	if len(notices) == 0 {
		b.WriteString("\nNo auto_deploy block is waiting for acceptance.")
	}
	for _, n := range notices {
		if n.Invalid {
			fmt.Fprintf(&b, "\n  %s: %s", n.Project, n.Message)
			continue
		}
		old := n.Old
		if old == "" {
			old = "(none accepted)"
		}
		fmt.Fprintf(&b, "\n  %s: %s\n    → %s", n.Project, old, n.New)
	}
	return errors.New(b.String())
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
	if _, _, err := buildGatewayConfig(cfg, rc, nil); err != nil {
		return err
	}
	if err := saveRemoteConfig(rc); err != nil {
		return err
	}
	if *noDeploy {
		return nil
	}
	return deployRemote(cfg, rc, rc.ComposeDir, nil)
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
	if err := deployRemote(cfg, rc, rc.ComposeDir, nil); err != nil {
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
	if gc, notices, err := buildGatewayConfig(cfg, rc, nil); err != nil {
		fmt.Printf("Projects:  error: %v\n", err)
	} else {
		autoDeploy := false
		for _, p := range gc.Projects {
			fmt.Printf("Project:   %s (%s)\n", p.Name, p.Tier)
			if ad := p.AutoDeploy; ad != nil {
				autoDeploy = true
				fmt.Printf("           auto-deploy %s@%s:%s\n", ad.Repo, ad.Branch, ad.ComposePath)
			}
		}
		for _, n := range notices {
			fmt.Printf("           %s\n", n.Message)
		}
		if autoDeploy || len(notices) > 0 || rc.GitHubToken != "" || rc.GitHubWebhookSecret != "" {
			fmt.Printf("GitHub:    token %s, webhook %s\n", setOrUnset(rc.GitHubToken != ""), onOrOff(rc.GitHubWebhookSecret != ""))
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

func setOrUnset(b bool) string {
	if b {
		return "set"
	}
	return "unset"
}

func onOrOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// autoDeployNotice reports an auto_deploy block that is not active because
// it is invalid, or
// the owner has not accepted it.
type autoDeployNotice struct {
	Dir     string
	Project string
	Message string
	// Invalid marks a block that can't be accepted as it is.
	Invalid bool
	// Old and New are the accepted and current acceptance fingerprints
	// ("" when none was accepted).
	Old, New string
}

// buildGatewayConfig assembles the gateway's config from the remote config
// and each project's towline.json. towline.json is agent-writable, so the
// project must belong to its directory and the tier must agree with the
// stack name; its token can only reach what Portainer already lets that
// project's team reach.
//
// An auto_deploy block is passed on only if the owner accepted exactly
// that block and compose policy (by fingerprint), or the project is named
// in accept, in which case its fingerprint is recorded in rc (the caller
// saves rc). Otherwise it is left out with a notice, so an agent can't
// point its project at another repository. An invalid block is an error
// only for a project being accepted; otherwise it is ignored with a notice.
func buildGatewayConfig(cfg *config.GlobalConfig, rc *remoteConfig, accept []string) (*gateway.Config, []autoDeployNotice, error) {
	portainerURL, _ := gatewayPortainerURL(cfg, rc)
	skip, _ := cfg.InsecureTLS()
	gc := &gateway.Config{
		PublicURL:       rc.URL,
		OwnerTokenHash:  rc.OwnerTokenHash,
		TrustCloudflare: rc.TunnelToken != "" && rc.Port == 0,
		NtfyURL:         rc.NtfyURL,
		GitHub:          gateway.GitHubConfig{Token: rc.GitHubToken, WebhookSecret: rc.GitHubWebhookSecret},
		Portainer:       gateway.PortainerConfig{URL: portainerURL, SkipTLSVerify: skip},
	}
	var notices []autoDeployNotice
	var acceptedNames []string
	for _, dir := range rc.Projects {
		pc, err := config.LoadProjectConfig(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", dir, err)
		}
		if err := checkProjectDirAnchor(cfg, dir, pc); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", dir, err)
		}
		if pc.MCPArgs.Token == "" || pc.StackName == "" {
			return nil, nil, fmt.Errorf("%s: towline.json is missing the token or stack name", dir)
		}
		tier := pc.Tier
		if m := stackTierSuffix.FindStringSubmatch(pc.StackName); m != nil && m[1] != tier {
			return nil, nil, fmt.Errorf("%s: stack %q does not match tier %q in towline.json", dir, pc.StackName, tier)
		}
		if !gateway.ValidProjectName(pc.StackName) {
			return nil, nil, fmt.Errorf("%s: stack name %q can't be served remotely (remote tool names need 1-24 lowercase letters, digits and dashes)", dir, pc.StackName)
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
		if ad := pc.AutoDeploy; ad != nil {
			accepting := slices.Contains(accept, p.Name) || slices.Contains(accept, filepath.Base(dir))
			if accepting {
				acceptedNames = append(acceptedNames, p.Name, filepath.Base(dir))
			}
			block := gateway.AutoDeployConfig{Repo: ad.Repo, Branch: ad.Branch, ComposePath: ad.ComposePath, CommitEnv: ad.CommitEnv}
			if ad.RequireChecks != nil {
				v := *ad.RequireChecks
				block.RequireChecks = &v
			}
			if err := block.Validate(); err != nil {
				if accepting {
					return nil, nil, fmt.Errorf("%s: %w", dir, err)
				}
				// The block is agent-writable: a bad one must not break
				// deploys of the gateway, only stay off.
				notices = append(notices, autoDeployNotice{Dir: dir, Project: p.Name, Invalid: true,
					Message: fmt.Sprintf("auto-deploy for %s is invalid, ignored: %v", p.Name, err)})
			} else {
				fp := acceptanceFingerprint(block, pc.ComposePolicy)
				switch {
				case accepting:
					if rc.AutoDeployAccepted == nil {
						rc.AutoDeployAccepted = map[string]string{}
					}
					rc.AutoDeployAccepted[dir] = fp
					p.AutoDeploy = &block
				case rc.AutoDeployAccepted[dir] == fp:
					p.AutoDeploy = &block
				default:
					notices = append(notices, autoDeployNotice{Dir: dir, Project: p.Name, Old: rc.AutoDeployAccepted[dir], New: fp, Message: fmt.Sprintf(
						"auto-deploy for %s (%s@%s:%s) is new or changed, or its compose policy changed, and stays off until you accept it: run 'towline remote deploy --accept-auto-deploy %s'",
						p.Name, block.Repo, block.Branch, block.ComposePath, p.Name)})
				}
			}
		}
		gc.Projects = append(gc.Projects, p)
	}
	for _, name := range accept {
		if !slices.Contains(acceptedNames, name) {
			return nil, nil, fmt.Errorf("--accept-auto-deploy: %s is not a served project with an auto_deploy block", name)
		}
	}
	if err := gc.Validate(); err != nil {
		return nil, nil, err
	}
	return gc, notices, nil
}

// acceptanceFingerprint is what the owner accepts for a project's
// auto-deploy: every field of the block, and the compose policy (also
// copied from the agent-writable towline.json), in a readable form.
func acceptanceFingerprint(b gateway.AutoDeployConfig, cp *config.ComposePolicyConfig) string {
	policy := "enforce"
	var binds, nets, vols []string
	if cp != nil {
		if cp.Mode != "" {
			policy = cp.Mode
		}
		binds = slices.Sorted(slices.Values(cp.AllowBindMounts))
		nets = slices.Sorted(slices.Values(cp.AllowNetworks))
		vols = slices.Sorted(slices.Values(cp.AllowVolumes))
	}
	return fmt.Sprintf("repo=%s branch=%s compose_path=%s require_checks=%t commit_env=%s compose_policy=%s bind_mounts=[%s] networks=[%s] volumes=[%s]",
		b.Repo, b.Branch, b.ComposePath, b.ChecksRequired(), b.CommitEnv, policy,
		strings.Join(binds, ","), strings.Join(nets, ","), strings.Join(vols, ","))
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

func deployRemote(cfg *config.GlobalConfig, rc *remoteConfig, composeDir string, accept []string) error {
	gc, notices, err := buildGatewayConfig(cfg, rc, accept)
	if err != nil {
		return err
	}
	printAutoDeploy(gc, notices)
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

// printAutoDeploy says what auto-deploy will do, and warns about settings
// that deploy without the owner's approval or without checks.
func printAutoDeploy(gc *gateway.Config, notices []autoDeployNotice) {
	enabled := false
	for _, p := range gc.Projects {
		ad := p.AutoDeploy
		if ad == nil {
			continue
		}
		enabled = true
		checks := "checks required"
		if !ad.ChecksRequired() {
			checks = "checks NOT required"
		}
		gated := p.Approval != gateway.ApprovalProd || p.Tier == "prod"
		approvalNote := "each deploy needs your approval on /ui"
		if !gated {
			approvalNote = "deploys without your approval"
		}
		fmt.Printf("Auto-deploy: %s deploys %s@%s:%s (%s; %s)\n", p.Name, ad.Repo, ad.Branch, ad.ComposePath, checks, approvalNote)
		if !gated {
			fmt.Printf("Warning: with approval 'prod', merges to %s@%s deploy to %s WITHOUT your approval.\n", ad.Repo, ad.Branch, p.Name)
		}
		if !ad.ChecksRequired() {
			fmt.Printf("Warning: %s deploys commits on %s@%s without waiting for their checks (require_checks is false).\n", p.Name, ad.Repo, ad.Branch)
		}
	}
	for _, n := range notices {
		fmt.Println("Auto-deploy: " + n.Message)
	}
	if !enabled {
		return
	}
	switch {
	case gc.GitHub.Token == "" && gc.GitHub.WebhookSecret == "":
		fmt.Println("Warning: no GitHub token or webhook: auto-deploy can read public repositories only, polling every 5 minutes. See 'towline remote github'.")
	case gc.GitHub.Token == "":
		fmt.Println("Note: no GitHub token: auto-deploy can read public repositories only. See 'towline remote github'.")
	}
}

// runRemoteGitHub sets the GitHub token and webhook secret auto-deploy
// uses, then redeploys.
func runRemoteGitHub(args []string) error {
	fs := flag.NewFlagSet("remote github", flag.ContinueOnError)
	tokenStdin := fs.Bool("token-stdin", false, "Read a read-only GitHub token from stdin (fine-grained: Contents, Commit statuses, Checks and Metadata, read-only)")
	clearToken := fs.Bool("clear-token", false, "Remove the GitHub token")
	webhook := fs.Bool("webhook", false, "Generate a new webhook secret and print the webhook settings for GitHub")
	clearWebhook := fs.Bool("clear-webhook", false, "Turn the webhook off")
	noDeploy := fs.Bool("no-deploy", false, "Only save the configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (the token is read from stdin with --token-stdin, never from the command line)", fs.Arg(0))
	}
	if !*tokenStdin && !*clearToken && !*webhook && !*clearWebhook {
		fs.Usage()
		return fmt.Errorf("nothing to do: pass --token-stdin, --clear-token, --webhook or --clear-webhook")
	}
	if (*tokenStdin && *clearToken) || (*webhook && *clearWebhook) {
		return fmt.Errorf("conflicting flags")
	}
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	rc, err := loadRemoteConfig()
	if err != nil {
		return err
	}
	switch {
	case *tokenStdin:
		if stdinIsTerminal() {
			fmt.Fprintln(promptOut, "Paste the token, then press Ctrl-D")
		}
		raw, err := io.ReadAll(io.LimitReader(readStdin, 4096))
		if err != nil {
			return fmt.Errorf("failed to read the token from stdin: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return fmt.Errorf("no token on stdin")
		}
		rc.GitHubToken = token
	case *clearToken:
		rc.GitHubToken = ""
	}
	secret := ""
	switch {
	case *webhook:
		secret = approval.NewToken()
		rc.GitHubWebhookSecret = secret
	case *clearWebhook:
		rc.GitHubWebhookSecret = ""
	}
	// Validates the token's shape without echoing it.
	if _, _, err := buildGatewayConfig(cfg, rc, nil); err != nil {
		return err
	}
	if !*noDeploy {
		if err := deployRemote(cfg, rc, rc.ComposeDir, nil); err != nil {
			return err
		}
	}
	if err := saveRemoteConfig(rc); err != nil {
		return err
	}
	fmt.Println("Saved ~/.towline/remote.yaml")
	if secret != "" {
		fmt.Println()
		fmt.Println("In the GitHub repository: Settings > Webhooks > Add webhook:")
		fmt.Printf("  Payload URL:   %s/hooks/github\n", rc.URL)
		fmt.Println("  Content type:  application/json")
		fmt.Println("  Secret (shown once):")
		fmt.Println()
		fmt.Println("  " + secret)
		fmt.Println()
		fmt.Println("  Events:        Pushes, Check suites and Statuses")
		fmt.Println("The webhook only makes the gateway check GitHub sooner; it can't choose what is deployed.")
	}
	if *noDeploy {
		fmt.Println("Run 'towline remote deploy' to apply it.")
	}
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
