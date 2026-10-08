package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/pkg/config"
)

// generatedFlags are the towline-mcp flags refresh owns. Any other flags in
// an existing config (e.g. -proxy, -caddy-api) are carried over.
var generatedFlags = map[string]bool{
	"-server": true, "-token": true, "-stack": true, "-tier": true,
	"-disable-version-check": true, "-skip-tls-verify": true,
	"-approval-mode": true, "-approval-webhook": true,
	"-compose-policy": true, "-allow-bind-mounts": true, "-allow-networks": true,
}

// valueFlags are generated flags that take a value argument.
var valueFlags = map[string]bool{
	"-server": true, "-token": true, "-stack": true, "-tier": true,
	"-approval-mode": true, "-approval-webhook": true,
	"-compose-policy": true, "-allow-bind-mounts": true, "-allow-networks": true,
}

// dangerousBindSources are never allowed automatically, even when an
// existing stack already mounts them: each gives control of the host.
var dangerousBindSources = []string{"/", "/var/run/docker.sock", "/run/docker.sock", "/var/lib/docker", "/etc", "/root", "/proc", "/sys", "/dev", "/boot", "/home"}

func runRefresh(args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	all := fs.Bool("all", false, "Refresh every project in the projects directory")
	keepRole := fs.Bool("keep-role", false, "Don't move the project team to the Standard user role")
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

	var dirs []string
	switch {
	case *all:
		entries, err := os.ReadDir(cfg.ProjectsDir)
		if err != nil {
			return fmt.Errorf("failed to read projects directory: %w", err)
		}
		for _, e := range entries {
			dir := filepath.Join(cfg.ProjectsDir, e.Name())
			if _, err := os.Stat(filepath.Join(dir, "towline.json")); e.IsDir() && err == nil {
				dirs = append(dirs, dir)
			}
		}
	case len(names) > 0:
		for _, name := range names {
			if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
				return fmt.Errorf("invalid project name %q", name)
			}
			dirs = append(dirs, filepath.Join(cfg.ProjectsDir, name))
		}
	default:
		cwd, _ := os.Getwd()
		if _, err := os.Stat(filepath.Join(cwd, "towline.json")); err != nil {
			return fmt.Errorf("no towline.json here; run inside a project, name projects, or use --all")
		}
		dirs = []string{cwd}
	}
	if len(dirs) == 0 {
		fmt.Println("No towline projects found.")
		return nil
	}

	api := newAdminAPI(cfg)
	failed := 0
	for _, dir := range dirs {
		fmt.Printf("Refreshing %s\n", dir)
		if err := refreshProject(cfg, api, dir, *keepRole); err != nil {
			failed++
			fmt.Printf("  error: %v\n", err)
		}
		fmt.Println()
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d projects could not be refreshed", failed, len(dirs))
	}
	fmt.Println("Done. Restart your agent sessions so they pick up the new MCP configuration.")
	return nil
}

// refreshProject brings a project's generated files up to date with this
// release without discarding the user's own changes.
func refreshProject(cfg *config.GlobalConfig, api *config.PortainerAPI, dir string, keepRole bool) error {
	pc, err := config.LoadProjectConfig(dir)
	if err != nil {
		return err
	}
	if pc.MCPArgs.Token == "" || pc.StackName == "" || (pc.Tier != "dev" && pc.Tier != "prod") {
		return fmt.Errorf("towline.json is missing the token, stack name, or tier")
	}
	projectName := projectNameFromStack(pc.StackName, pc.Tier)

	// Grandfather the stack's existing bind mounts so it keeps deploying
	// under the compose policy (only on the first refresh).
	// towline.json is agent-writable. When the project lives in the projects
	// directory, its stack name must match the directory (as destroy
	// requires) before its IDs are used against Portainer.
	anchorErr := checkProjectDirAnchor(cfg, dir, pc)
	if anchorErr != nil {
		fmt.Printf("  warning: %v; skipped compose policy grandfathering and team role update\n", anchorErr)
	}

	// Grandfathering reads the stack with the project's own token, so it
	// only ever sees a stack Portainer already lets this project read.
	if anchorErr == nil {
		projectAPI := newProjectAPI(cfg, pc.MCPArgs.Token)
		if pc.ComposePolicy == nil {
			pc.ComposePolicy = grandfatherComposePolicy(cfg, projectAPI, pc)
		} else if pc.ComposePolicy.AllowNetworks == nil {
			grandfatherNetworks(cfg, projectAPI, pc)
		}
	}

	if !keepRole && anchorErr == nil {
		migrateTeamRole(cfg, api, pc)
	}

	binary := resolveMCPBinary()
	if binary == "towline-mcp" && pc.MCPBinary != "" {
		if _, err := os.Stat(pc.MCPBinary); err == nil {
			binary = pc.MCPBinary
		}
	}
	pc.MCPBinary = binary

	data := projectTemplateData(cfg, projectName, pc, pc.MCPArgs.Token, binary)
	serverName := "towline-" + pc.Tier
	for _, f := range []struct{ tmpl, rel string }{
		{"mcp-json.tmpl", ".mcp.json"},
		{"cursor-mcp.tmpl", filepath.Join(".cursor", "mcp.json")},
		{"gemini-settings.tmpl", filepath.Join(".gemini", "settings.json")},
	} {
		if err := mergeMCPServer(filepath.Join(dir, f.rel), f.tmpl, serverName, data); err != nil {
			return fmt.Errorf("failed to update %s: %w", f.rel, err)
		}
	}
	fmt.Println("  MCP configs updated (token moved to env, approval and compose settings applied)")

	if err := ensureClaudePermission(filepath.Join(dir, ".claude", "settings.json"), "mcp__"+serverName); err != nil {
		return fmt.Errorf("failed to update .claude/settings.json: %w", err)
	}

	if err := ensureGitignore(dir); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}
	for _, d := range []string{".claude", ".cursor", ".gemini"} {
		if info, err := os.Stat(filepath.Join(dir, d)); err == nil && info.IsDir() {
			_ = os.Chmod(filepath.Join(dir, d), secretDirMode)
		}
	}
	if err := config.SaveProjectConfig(dir, pc); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Join(dir, "towline.json"), secretFileMode)
	fmt.Println("  Permissions and .gitignore updated")

	warnTrackedSecrets(dir)
	return nil
}

// grandfatherComposePolicy allows the bind mounts the deployed stack already
// uses, except ones that would hand over the host, and reports anything
// else the compose policy would reject on the next update.
//
// towline.json is agent-writable, so api should carry the project's own
// token, and the stack must carry the project's stack name on the
// configured environment.
func grandfatherComposePolicy(cfg *config.GlobalConfig, api *config.PortainerAPI, pc *config.ProjectConfig) *config.ComposePolicyConfig {
	policy := &config.ComposePolicyConfig{Mode: "enforce", AllowNetworks: []string{}}
	if pc.StackID == 0 {
		return policy
	}
	stackName, compose, ok := readProjectStack(cfg, api, pc)
	if !ok {
		return nil // retry on the next refresh
	}

	var refused []string
	for _, src := range middleware.BindMountSources(compose) {
		switch {
		case isDangerousBind(src):
			refused = append(refused, src)
		case strings.HasPrefix(src, "/"):
			policy.AllowBindMounts = appendUnique(policy.AllowBindMounts, path.Clean(src))
		case !strings.ContainsAny(src, "$~"):
			policy.AllowBindMounts = appendUnique(policy.AllowBindMounts, ".")
		}
	}
	if len(policy.AllowBindMounts) > 0 {
		fmt.Printf("  Compose policy: allowing the bind mounts stack %q already uses:\n", stackName)
		for _, m := range policy.AllowBindMounts {
			fmt.Println("    - " + m)
		}
	}
	policy.AllowNetworks = existingNetworks(compose)

	remaining := middleware.ComposePolicy{AllowBindMounts: policy.AllowBindMounts, AllowNetworks: policy.AllowNetworks, StackName: pc.StackName}.Validate(compose)
	if len(remaining) > 0 {
		fmt.Println("  warning: the deployed compose file uses settings the compose policy rejects, so the")
		fmt.Println("  agent's next updateLocalStack will fail until they are removed:")
		for _, v := range remaining {
			fmt.Println("    - " + v)
		}
		if len(refused) > 0 {
			fmt.Printf("  (%s not allowed automatically: these mounts give control of the host)\n", strings.Join(refused, ", "))
		}
		fmt.Println("  If the stack really needs them, set compose_policy in towline.json (allow_bind_mounts,")
		fmt.Println("  allow_networks, or \"mode\": \"off\") and run 'towline refresh' again.")
	}
	return policy
}

// grandfatherNetworks allows the external networks the deployed stack
// already joins, for projects whose compose policy predates allow_networks.
func grandfatherNetworks(cfg *config.GlobalConfig, api *config.PortainerAPI, pc *config.ProjectConfig) {
	if pc.StackID == 0 {
		pc.ComposePolicy.AllowNetworks = []string{}
		return
	}
	_, compose, ok := readProjectStack(cfg, api, pc)
	if !ok {
		return // retry on the next refresh
	}
	pc.ComposePolicy.AllowNetworks = existingNetworks(compose)
}

// readProjectStack reads the deployed compose file for grandfathering. The
// IDs come from agent-writable towline.json, so api should carry the
// project's own token, and the stack must carry the project's stack name on
// the configured environment.
func readProjectStack(cfg *config.GlobalConfig, api *config.PortainerAPI, pc *config.ProjectConfig) (name, compose string, ok bool) {
	stack, err := api.GetStack(pc.StackID)
	if err != nil {
		fmt.Printf("  warning: could not look up stack %d to check the compose policy: %v\n", pc.StackID, err)
		return "", "", false
	}
	if stack.Name != pc.StackName || stack.EndpointID != cfg.PortainerEnvID {
		fmt.Printf("  warning: skipped compose policy check: stack %d is %q on environment %d, expected %q on environment %d\n",
			pc.StackID, stack.Name, stack.EndpointID, pc.StackName, cfg.PortainerEnvID)
		return "", "", false
	}
	compose, err = api.GetStackFile(stack.ID)
	if err != nil {
		fmt.Printf("  warning: could not read the deployed compose file to check the compose policy: %v\n", err)
		return "", "", false
	}
	return stack.Name, compose, true
}

// existingNetworks returns the external networks a compose file joins that
// may be allowed automatically (never host, none or bridge), as a non-nil
// list so towline.json records that grandfathering ran.
func existingNetworks(compose string) []string {
	allowed := []string{}
	for _, name := range middleware.ExternalNetworkNames(compose) {
		if name != "host" && name != "none" && name != "bridge" && !strings.Contains(name, "$") {
			allowed = appendUnique(allowed, name)
		}
	}
	if len(allowed) > 0 {
		fmt.Printf("  Compose policy: allowed existing external networks %s\n", strings.Join(allowed, ", "))
	}
	return allowed
}

// checkProjectDirAnchor requires, for a project directory directly inside
// the projects directory, that towline.json's stack name is
// <directory name>-dev or -prod. Projects elsewhere (existing codebases)
// have no such anchor and are not checked here.
func checkProjectDirAnchor(cfg *config.GlobalConfig, dir string, pc *config.ProjectConfig) error {
	if cfg.ProjectsDir == "" || filepath.Dir(resolvePath(dir)) != resolvePath(cfg.ProjectsDir) {
		return nil
	}
	name := filepath.Base(resolvePath(dir))
	if pc.StackName != name+"-dev" && pc.StackName != name+"-prod" {
		return fmt.Errorf("towline.json stack_name %q does not belong to project %q", pc.StackName, name)
	}
	return nil
}

// resolvePath returns an absolute, symlink-free form of p where possible.
func resolvePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p)
}

func isDangerousBind(src string) bool {
	if !strings.HasPrefix(src, "/") {
		return false
	}
	clean := path.Clean(src)
	for _, d := range dangerousBindSources {
		if clean == d {
			return true
		}
	}
	return strings.HasSuffix(clean, "/docker.sock")
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// migrateTeamRole moves a project team from Environment administrator to
// Standard user, after checking the team is the one towline created.
func migrateTeamRole(cfg *config.GlobalConfig, api *config.PortainerAPI, pc *config.ProjectConfig) {
	if pc.TeamID == 0 {
		return
	}
	// towline.json is agent-writable: only touch the environment towline is
	// configured for, and only a team carrying this project's name.
	if pc.EnvID != 0 && pc.EnvID != cfg.PortainerEnvID {
		fmt.Printf("  warning: skipped team role update (towline.json environment %d is not the configured environment %d)\n", pc.EnvID, cfg.PortainerEnvID)
		return
	}
	want := "team-" + pc.StackName
	name, err := api.GetTeamName(pc.TeamID)
	if err != nil || name != want {
		fmt.Printf("  warning: skipped team role update (team %d is %q, expected %q)\n", pc.TeamID, name, want)
		return
	}
	if err := api.SetEndpointTeamAccess(cfg.PortainerEnvID, pc.TeamID); err != nil {
		fmt.Printf("  warning: could not update team role: %v\n", err)
		return
	}
	fmt.Println("  Team role set to Standard user")
}

// mergeMCPServer renders a template and replaces only the towline server
// entry in an existing MCP config, keeping other servers, extra flags such
// as -proxy, and extra env vars the user added.
func mergeMCPServer(file, tmpl, serverName string, data TemplateData) error {
	rendered, err := renderTemplateToString(tmpl, data)
	if err != nil {
		return err
	}
	var fresh struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(rendered), &fresh); err != nil {
		return fmt.Errorf("template %s produced invalid JSON: %w", tmpl, err)
	}
	entry := fresh.MCPServers[serverName]

	existing := map[string]any{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &existing); err != nil {
			// Don't drop the user's other servers: let them fix the file.
			return fmt.Errorf("%s is not valid JSON (fix or remove it and retry): %w", file, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	servers, _ := existing["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if old, ok := servers[serverName].(map[string]any); ok {
		carryOver(old, entry)
	}
	servers[serverName] = entry
	existing["mcpServers"] = servers

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return writeFileMode(file, append(out, '\n'), secretFileMode)
}

// carryOver copies user-added flags and env vars from an old server entry.
func carryOver(old, entry map[string]any) {
	oldArgs, _ := old["args"].([]any)
	newArgs, _ := entry["args"].([]any)
	for i := 0; i < len(oldArgs); i++ {
		arg, _ := oldArgs[i].(string)
		if name, inline := normalizeFlag(arg); generatedFlags[name] {
			// -flag=value carries its value; -flag value takes the next arg.
			if valueFlags[name] && !inline {
				i++
			}
			continue
		}
		newArgs = append(newArgs, oldArgs[i])
		// Keep a flag's value with it.
		if strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") && i+1 < len(oldArgs) {
			if next, _ := oldArgs[i+1].(string); !strings.HasPrefix(next, "-") {
				newArgs = append(newArgs, oldArgs[i+1])
				i++
			}
		}
	}
	entry["args"] = newArgs

	oldEnv, _ := old["env"].(map[string]any)
	newEnv, _ := entry["env"].(map[string]any)
	for k, v := range oldEnv {
		if _, ok := newEnv[k]; !ok && k != "TOWLINE_APPROVAL_WEBHOOK_TOKEN" {
			newEnv[k] = v
		}
	}
}

// normalizeFlag returns a Go-style flag argument as "-name" (accepting
// -name, --name and either with =value) and whether it carries its value.
// Non-flag arguments are returned unchanged.
func normalizeFlag(arg string) (name string, inlineValue bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return arg, false
	}
	name = "-" + strings.TrimLeft(arg, "-")
	if i := strings.Index(name, "="); i >= 0 {
		return name[:i], true
	}
	return name, false
}

// ensureClaudePermission makes sure .claude/settings.json allows the towline
// MCP server, keeping any other settings.
func ensureClaudePermission(file, permission string) error {
	settings := map[string]any{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	for _, a := range allow {
		if a == permission {
			return os.Chmod(file, secretFileMode)
		}
	}
	perms["allow"] = append(allow, permission)
	settings["permissions"] = perms

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeFileMode(file, append(out, '\n'), secretFileMode)
}

// warnTrackedSecrets warns if files holding the project token are
// committed to git: .gitignore does not untrack them.
func warnTrackedSecrets(dir string) {
	tracked := trackedSecrets(dir)
	if len(tracked) == 0 {
		return
	}
	fmt.Println("  warning: these files contain the project's Portainer token and are tracked by git:")
	for _, f := range tracked {
		fmt.Println("    - " + f)
	}
	fmt.Println("  Untrack them with 'git rm --cached <file>'. If the repository was ever pushed, treat the")
	fmt.Println("  token as leaked: revoke it in Portainer (Users > towline-" + filepath.Base(dir) + "-* > Access tokens).")
}

// trackedSecrets lists the token-bearing files in dir that git tracks. It
// returns nil when dir is not a git work tree or git is unavailable.
func trackedSecrets(dir string) []string {
	out, err := exec.Command("git", "-C", dir, "ls-files", "--", ".mcp.json", "towline.json", ".claude", ".cursor", ".gemini").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}
