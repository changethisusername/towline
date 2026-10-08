package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
// read from the Portainer host filesystem, and volumes or networks that
// belong to other projects.
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
					"\nIf the stack genuinely needs this, ask your human partner: they can allow specific bind mounts or external networks (or disable the policy) under compose_policy in towline.json and run 'towline refresh'."), nil
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
	// AllowNetworks lists external networks (by effective name: "name" if
	// set, else the key) that stacks may join, e.g. a shared proxy network.
	// host, none and bridge are never allowed.
	AllowNetworks []string
}

// reservedNetworkNames are Docker's built-in networks; joining them as an
// external network is never allowed, even when listed in AllowNetworks.
var reservedNetworkNames = map[string]bool{"host": true, "none": true, "bridge": true}

// allowsNetwork reports whether an external network name is on the allowlist.
func (p ComposePolicy) allowsNetwork(name string) bool {
	if name == "" || reservedNetworkNames[name] || strings.Contains(name, "$") {
		return false
	}
	for _, allowed := range p.AllowNetworks {
		if allowed == name {
			return true
		}
	}
	return false
}

// externalNetworkName returns the effective name of a top-level network
// that refers to a network outside the stack ("external" or "name" set),
// and whether it does. The name is "" if it is not a plain string.
func externalNetworkName(key string, net map[string]any) (string, bool) {
	_, hasExternal := net["external"]
	rawName, hasName := net["name"]
	if !hasExternal && !hasName {
		return "", false
	}
	if !hasName {
		return key, true
	}
	name, _ := rawName.(string)
	return name, true
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
	// Compose merges every "---" document in a file, so a policy that only
	// read the first one could be bypassed by a later document.
	var doc map[string]any
	dec := yaml.NewDecoder(bytes.NewReader([]byte(content)))
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return []string{fmt.Sprintf("compose file is not valid YAML: %v", err)}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return []string{"compose file must contain a single YAML document (no \"---\" separators)"}
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
			for _, key := range []string{"external", "name"} {
				if _, ok := vol[key]; ok {
					add("volume %q: %q is not allowed (it can mount another project's volume)", name, key)
				}
			}
		}
	}

	if nets, ok := doc["networks"].(map[string]any); ok {
		for name, raw := range nets {
			net, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if ext, ok := externalNetworkName(name, net); ok {
				switch {
				case reservedNetworkNames[ext]:
					add("network %q: joining the %q network is not allowed", name, ext)
				case !p.allowsNetwork(ext):
					add("network %q: external network %q is not allowed (it can join another project's network); a human can allow it in compose_policy.allow_networks", name, ext)
				}
			}
			if _, ok := net["driver_opts"]; ok {
				add("network %q: driver_opts are not allowed", name)
			}
			if d, ok := net["driver"]; ok && d != "bridge" && d != "overlay" {
				add("network %q: only the bridge and overlay network drivers are allowed", name)
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
// network_mode is checked separately against an allowlist.
var hostModeKeys = []string{"pid", "ipc", "uts", "userns_mode", "cgroup"}

// allowedNetworkModes are the network_mode values that cannot attach a
// service to the host or to a network or container outside the stack.
var allowedNetworkModes = map[string]bool{"bridge": true, "none": true}

// lifecycleHookKeys hold commands compose runs inside the container; each
// hook can request privileged mode independently of the service.
var lifecycleHookKeys = []string{"post_start", "pre_stop"}

// gpuCapabilities are the device reservation capabilities allowed in
// deploy.resources.reservations.devices (GPU access via the NVIDIA runtime).
var gpuCapabilities = map[string]bool{"gpu": true, "compute": true, "utility": true, "graphics": true, "video": true, "display": true, "compat32": true}

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

	if val, ok := svc["network_mode"]; ok {
		if s, isString := val.(string); !isString || !allowedNetworkModes[s] {
			add("network_mode %q is not allowed", fmt.Sprint(val))
		}
	}

	for _, key := range lifecycleHookKeys {
		raw, ok := svc[key]
		if !ok {
			continue
		}
		hooks, isList := raw.([]any)
		if !isList {
			add("%s must be a list of hooks", key)
			continue
		}
		for _, h := range hooks {
			hook, _ := h.(map[string]any)
			if p, ok := hook["privileged"]; ok {
				if b, isBool := p.(bool); !isBool || b {
					add("%s hook privileged mode is not allowed", key)
				}
			}
		}
	}

	for _, msg := range checkDeviceReservations(svc["deploy"]) {
		add("%s", msg)
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

// checkDeviceReservations rejects deploy.resources.reservations.devices
// entries other than GPU reservations: like "devices", other drivers (e.g.
// CDI) and options can give the container host devices.
func checkDeviceReservations(deploy any) []string {
	d, _ := deploy.(map[string]any)
	resources, _ := d["resources"].(map[string]any)
	reservations, _ := resources["reservations"].(map[string]any)
	raw, ok := reservations["devices"]
	if !ok {
		return nil
	}
	const msg = "deploy.resources.reservations.devices %s is not allowed; only GPU reservations (capabilities: [gpu], driver nvidia) are permitted"
	devices, isList := raw.([]any)
	if !isList {
		return []string{fmt.Sprintf(msg, "value")}
	}
	var v []string
	for _, rawDev := range devices {
		dev, isMap := rawDev.(map[string]any)
		if !isMap {
			v = append(v, fmt.Sprintf(msg, "entry"))
			continue
		}
		for key, val := range dev {
			switch key {
			case "count", "device_ids":
			case "driver":
				if val != "nvidia" {
					v = append(v, fmt.Sprintf(msg, fmt.Sprintf("driver %q", fmt.Sprint(val))))
				}
			case "capabilities":
			default:
				v = append(v, fmt.Sprintf(msg, fmt.Sprintf("%q", key)))
			}
		}
		caps, _ := dev["capabilities"].([]any)
		if len(caps) == 0 {
			v = append(v, fmt.Sprintf(msg, "entry without capabilities"))
		}
		for _, c := range caps {
			if s, _ := c.(string); !gpuCapabilities[s] {
				v = append(v, fmt.Sprintf(msg, fmt.Sprintf("capability %q", fmt.Sprint(c))))
			}
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

// ExternalNetworkNames returns the effective names of top-level networks
// that refer to networks outside the stack ("external" or "name" set), in
// key order. Names that are not plain strings are skipped.
func ExternalNetworkNames(content string) []string {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	nets, _ := doc["networks"].(map[string]any)
	keys := make([]string, 0, len(nets))
	for key := range nets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out []string
	for _, key := range keys {
		net, _ := nets[key].(map[string]any)
		if name, ok := externalNetworkName(key, net); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
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
