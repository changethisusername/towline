package middleware

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

// NewComposePolicy returns a middleware that validates compose files submitted
// via createLocalStack and updateLocalStack. It rejects settings that would let
// a stack escape its container sandbox: privileged mode, host namespaces, host
// bind mounts, device access, added capabilities, and file references that
// read from the Portainer host filesystem.
func NewComposePolicy(toolName string, policy ComposePolicy) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		if policy.Disabled || (toolName != "createLocalStack" && toolName != "updateLocalStack") {
			return next
		}
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			parser := toolgen.NewParameterParser(request)
			file, err := parser.GetString("file", true)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("invalid file parameter", err), nil
			}
			if violations := policy.Validate(file); len(violations) > 0 {
				return mcp.NewToolResultError("Compose file rejected by security policy:\n- " + strings.Join(violations, "\n- ") +
					"\nIf the stack genuinely needs this, ask your human partner: they can allow specific bind mounts (or disable the policy) under compose_policy in towline.json and run 'towline refresh'."), nil
			}
			return next(ctx, request)
		}
	}
}

// ComposePolicy configures compose validation for a project. The zero value
// is the strict default.
type ComposePolicy struct {
	// Disabled turns validation off entirely (set by a human per project).
	Disabled bool
	// AllowBindMounts lists host paths that may be bind mounted: an absolute
	// path allows itself and everything below it; "." allows relative paths
	// inside the stack directory.
	AllowBindMounts []string
}

// allowsBind reports whether a bind mount source is on the allowlist.
func (p ComposePolicy) allowsBind(source string) bool {
	if source == "" || strings.ContainsAny(source, "$~\\") {
		return false
	}
	for _, allowed := range p.AllowBindMounts {
		if allowed == "." {
			if !strings.HasPrefix(source, "/") && isSafeRelativePath(source) {
				return true
			}
			continue
		}
		if !strings.HasPrefix(allowed, "/") || !strings.HasPrefix(source, "/") {
			continue
		}
		a, s := path.Clean(allowed), path.Clean(source)
		if s == a || (a == "/" || strings.HasPrefix(s, a+"/")) {
			return true
		}
	}
	return false
}

// ValidateCompose checks compose content against the strict default policy.
func ValidateCompose(content string) []string {
	return ComposePolicy{}.Validate(content)
}

// Validate checks compose content against the scoped-stack security policy
// and returns a list of human-readable violations (empty if allowed).
func (p ComposePolicy) Validate(content string) []string {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return []string{fmt.Sprintf("compose file is not valid YAML: %v", err)}
	}

	var v []string
	add := func(format string, args ...any) { v = append(v, fmt.Sprintf(format, args...)) }

	for _, key := range []string{"include", "extends"} {
		if _, ok := doc[key]; ok {
			add("top-level %q is not allowed", key)
		}
	}

	services, _ := doc["services"].(map[string]any)
	for name, raw := range services {
		svc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		v = append(v, p.validateService(name, svc)...)
	}

	if vols, ok := doc["volumes"].(map[string]any); ok {
		for name, raw := range vols {
			vol, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := vol["driver_opts"]; ok {
				add("volume %q: driver_opts are not allowed (can bind host paths)", name)
			}
			if d, ok := vol["driver"].(string); ok && d != "local" {
				add("volume %q: only the local volume driver is allowed", name)
			}
		}
	}

	for _, section := range []string{"secrets", "configs"} {
		entries, _ := doc[section].(map[string]any)
		for name, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := entry["file"]; ok {
				add("%s %q: file sources are not allowed (they read from the Portainer host)", section, name)
			}
		}
	}

	return v
}

// hostModeKeys are service keys where "host" or "container:*" shares a
// namespace with the host or with a container outside the stack.
var hostModeKeys = []string{"network_mode", "pid", "ipc", "uts", "userns_mode", "cgroup"}

// forbiddenServiceKeys grant host-level privileges regardless of value.
var forbiddenServiceKeys = []string{"cap_add", "devices", "device_cgroup_rules", "cgroup_parent", "extends", "gpus", "external_links"}

// forbiddenBuildKeys give a build access to the host or to host secrets.
var forbiddenBuildKeys = []string{"additional_contexts", "network", "entitlements", "privileged", "ssh", "secrets"}

func (p ComposePolicy) validateService(name string, svc map[string]any) []string {
	var v []string
	add := func(format string, args ...any) {
		v = append(v, fmt.Sprintf("service %q: ", name)+fmt.Sprintf(format, args...))
	}

	if p, ok := svc["privileged"]; ok {
		if b, isBool := p.(bool); !isBool || b {
			add("privileged mode is not allowed")
		}
	}

	for _, key := range forbiddenServiceKeys {
		if _, ok := svc[key]; ok {
			add("%q is not allowed", key)
		}
	}

	for _, key := range hostModeKeys {
		val, ok := svc[key]
		if !ok {
			continue
		}
		s, _ := val.(string)
		if s == "host" || strings.HasPrefix(s, "container:") || strings.HasPrefix(s, "service:") || strings.Contains(s, "$") {
			add("%s %q is not allowed", key, s)
		}
	}

	if opts, ok := svc["security_opt"].([]any); ok {
		for _, o := range opts {
			s, _ := o.(string)
			if strings.Contains(s, "unconfined") || strings.Contains(s, "disable") || strings.Contains(s, "$") {
				add("security_opt %q is not allowed", s)
			}
		}
	}

	if files, ok := svc["env_file"]; ok {
		for _, f := range envFilePaths(files) {
			if !isSafeRelativePath(f) {
				add("env_file %q must be a relative path inside the stack", f)
			}
		}
	}

	if vols, ok := svc["volumes"].([]any); ok {
		for _, raw := range vols {
			if msg := p.checkServiceVolume(raw); msg != "" {
				add("%s", msg)
			}
		}
	}

	if _, ok := svc["volumes_from"]; ok {
		add("volumes_from is not allowed")
	}

	if build, ok := svc["build"]; ok {
		for _, msg := range checkBuild(build) {
			add("%s", msg)
		}
	}

	return v
}

// checkBuild rejects builds from a local context: on a Portainer string
// stack that is Portainer's own filesystem, so the image could capture
// Portainer's database and keys. Remote (git/URL) contexts are allowed.
func checkBuild(raw any) []string {
	var context string
	switch b := raw.(type) {
	case string:
		context = b
	case map[string]any:
		context, _ = b["context"].(string)
		var v []string
		for _, key := range forbiddenBuildKeys {
			if _, ok := b[key]; ok {
				v = append(v, fmt.Sprintf("build %q is not allowed", key))
			}
		}
		if df, ok := b["dockerfile"].(string); ok && !isSafeRelativePath(df) {
			v = append(v, fmt.Sprintf("build dockerfile %q must be a relative path", df))
		}
		if !isRemoteBuildContext(context) {
			v = append(v, fmt.Sprintf("build context %q is not allowed; use a git or https URL, or a prebuilt image", context))
		}
		return v
	}
	if !isRemoteBuildContext(context) {
		return []string{fmt.Sprintf("build context %q is not allowed; use a git or https URL, or a prebuilt image", context)}
	}
	return nil
}

func isRemoteBuildContext(ctx string) bool {
	if strings.Contains(ctx, "$") {
		return false
	}
	return strings.HasPrefix(ctx, "https://") || strings.HasPrefix(ctx, "http://") || strings.HasPrefix(ctx, "git@") || strings.HasPrefix(ctx, "git://")
}

// checkServiceVolume returns a violation message if a service volume entry is
// a bind mount (or cannot be proven to be a named volume / tmpfs).
func (p ComposePolicy) checkServiceVolume(raw any) string {
	switch vol := raw.(type) {
	case string:
		source, _, hasTarget := strings.Cut(vol, ":")
		if !hasTarget {
			return "" // anonymous volume
		}
		if !isNamedVolume(source) && !p.allowsBind(source) {
			return fmt.Sprintf("bind mount %q is not allowed; use a named volume", vol)
		}
	case map[string]any:
		typ, _ := vol["type"].(string)
		source, _ := vol["source"].(string)
		switch typ {
		case "volume", "":
			if source != "" && !isNamedVolume(source) && !p.allowsBind(source) {
				return fmt.Sprintf("volume source %q is not allowed; use a named volume", source)
			}
		case "tmpfs":
		case "bind":
			if !p.allowsBind(source) {
				return fmt.Sprintf("bind mount of %q is not allowed; use a named volume", source)
			}
		default:
			return fmt.Sprintf("volume type %q is not allowed; use a named volume", typ)
		}
	default:
		return "unrecognized volume entry"
	}
	return ""
}

// isNamedVolume reports whether a short-syntax volume source is a named
// volume rather than a host path or an interpolated value.
func isNamedVolume(source string) bool {
	if source == "" || strings.ContainsAny(source, "/\\$~") || strings.HasPrefix(source, ".") {
		return false
	}
	return true
}

func envFilePaths(raw any) []string {
	switch f := raw.(type) {
	case string:
		return []string{f}
	case []any:
		var out []string
		for _, item := range f {
			switch e := item.(type) {
			case string:
				out = append(out, e)
			case map[string]any:
				if p, ok := e["path"].(string); ok {
					out = append(out, p)
				}
			}
		}
		return out
	}
	return nil
}

func isSafeRelativePath(p string) bool {
	if p == "" || strings.ContainsAny(p, "$~\\") || strings.HasPrefix(p, "/") {
		return false
	}
	clean := path.Clean(p)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// BindMountSources returns the host paths bind mounted by services in a
// compose file (short and long volume syntax), in order of appearance.
func BindMountSources(content string) []string {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	services, _ := doc["services"].(map[string]any)
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		svc, _ := services[name].(map[string]any)
		vols, _ := svc["volumes"].([]any)
		for _, raw := range vols {
			var source string
			switch vol := raw.(type) {
			case string:
				src, _, hasTarget := strings.Cut(vol, ":")
				if hasTarget && !isNamedVolume(src) {
					source = src
				}
			case map[string]any:
				typ, _ := vol["type"].(string)
				src, _ := vol["source"].(string)
				if typ == "bind" || ((typ == "" || typ == "volume") && src != "" && !isNamedVolume(src)) {
					source = src
				}
			}
			if source != "" && !seen[source] {
				seen[source] = true
				out = append(out, source)
			}
		}
	}
	return out
}
