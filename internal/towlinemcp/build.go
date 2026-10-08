// Package towlinemcp assembles a towline MCP server for one project: the
// upstream Portainer MCP tools and the towline tools, wrapped in towline's
// middleware chain. The stdio binary and the remote gateway share it, so
// both apply exactly the same checks.
package towlinemcp

import (
	"fmt"
	"strings"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/internal/proxy"
	"github.com/changethisusername/towline/internal/tooldef"
	"github.com/changethisusername/towline/internal/towline"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

// Options configures one project's server.
type Options struct {
	ServerURL           string
	Token               string
	ToolsPath           string
	TowlineToolsPath    string
	ReadOnly            bool
	DisableVersionCheck bool
	SkipTLSVerify       bool

	Stack string
	Tier  middleware.Tier
	// GatingTier is the tier used for approval gating. It defaults to Tier;
	// the remote gateway sets it to prod so that changes made by remote
	// clients need approval on dev projects too.
	GatingTier middleware.Tier

	Proxy    string
	CaddyAPI string

	ApprovalMode middleware.ApprovalMode
	// Approver receives human approval requests (nil refuses them).
	Approver approval.Approver
	// ApprovalURL is the page where approvals are decided, shown to the agent.
	ApprovalURL string

	ComposePolicy middleware.ComposePolicy

	// RequireCaller refuses tool calls that carry no authenticated caller
	// (remote transports).
	RequireCaller bool
}

// Instance is a built project server.
type Instance struct {
	Server *mcp.PortainerMCPServer
	Store  *approval.Store
}

// Close stops background work.
func (i *Instance) Close() {
	i.Store.Stop()
}

// Build creates the server for one project and registers its tools.
func Build(o Options) (*Instance, error) {
	if o.ServerURL == "" || o.Token == "" {
		return nil, fmt.Errorf("a Portainer URL and token are required")
	}
	if o.Stack == "" {
		return nil, fmt.Errorf("a stack name is required")
	}
	if o.Tier != middleware.TierDev && o.Tier != middleware.TierProd {
		return nil, fmt.Errorf("tier must be 'dev' or 'prod'")
	}
	gatingTier := o.GatingTier
	if gatingTier == "" {
		gatingTier = o.Tier
	}
	if o.CaddyAPI == "" {
		o.CaddyAPI = "http://localhost:2019"
	}
	o.ComposePolicy.StackName = o.Stack

	// Tools files written by an older version are upgraded in place (with
	// a .bak copy) so projects pick up new tool definitions.
	status, err := tooldef.EnsureToolsFile(o.ToolsPath, tooldef.ToolsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare tools.yaml file: %w", err)
	}
	log.Info().Str("path", o.ToolsPath).Msg("tools file " + status)
	status, err = tooldef.EnsureToolsFile(o.TowlineToolsPath, tooldef.TowlineToolsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare towline-tools.yaml file: %w", err)
	}
	log.Info().Str("path", o.TowlineToolsPath).Msg("tools file " + status)

	srv, err := mcp.NewPortainerMCPServer(
		o.ServerURL, o.Token, o.ToolsPath,
		mcp.WithReadOnly(o.ReadOnly),
		mcp.WithDisableVersionCheck(o.DisableVersionCheck),
		mcp.WithSkipTLSVerify(o.SkipTLSVerify),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	towlineTools, err := toolgen.LoadToolsFromYAML(o.TowlineToolsPath, "v1.0.0")
	if err != nil {
		return nil, fmt.Errorf("failed to load towline tools: %w", err)
	}
	srv.MergeTools(towlineTools)

	approvalStore := approval.NewStore()
	gate := &middleware.ApprovalGate{Mode: o.ApprovalMode, Store: approvalStore, Project: o.Stack, Approver: o.Approver, ApprovalURL: o.ApprovalURL}

	// The stack ID and environment ID are resolved at startup; while either
	// is unknown (the stack is not created yet, or Portainer was
	// unreachable) they are looked up again on use.
	stackScoping := middleware.NewStackScoping(o.Stack, middleware.WithStackLookup(func() (int, bool) {
		s, ok := findLocalStack(srv, o.Stack)
		return s.ID, ok
	}))
	resolveStackID(srv, stackScoping, o.Stack)

	// Helper to create proxy function for ownership checks. HTTP error
	// statuses come back as errors, never as data.
	proxyFn := towline.NewProxyFunc(srv.Client().ProxyDockerRequest)

	// Get environment ID from the stack, or 0 until the stack exists
	envID := getEnvironmentID(srv, o.Stack)
	envResolver := middleware.NewEnvResolver(envID, func() int { return getEnvironmentID(srv, o.Stack) })
	ownership := middleware.NewContainerOwnership(o.Stack, envID, proxyFn, middleware.WithEnvResolver(envResolver))

	handlers := towline.NewHandlers(srv, o.Stack, envID, proxyFn)

	// Build middleware chain for a given tool
	wrappers := func(toolName string) []func(mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
		var mws []func(mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc

		// Outermost: the remote caller's scope (a no-op for stdio)
		mws = append(mws, middleware.NewCallerGate(toolName, o.RequireCaller))

		// Compose security policy, so a stack that would be rejected
		// anyway never reaches the approval flow
		mws = append(mws, middleware.NewComposePolicy(toolName, o.ComposePolicy))

		// Tier gating
		mws = append(mws, middleware.NewTierGating(gatingTier, gate, toolName))

		// Stack scoping
		mws = append(mws, stackScoping.ForTool(toolName))

		// Env filter: strip agent-supplied _TOWLINE_ env vars from create/update
		// requests, and carry the stack's own internal vars over on update
		mws = append(mws, middleware.NewEnvFilter(toolName, handlers.PrepareStackUpdate))

		// Container ownership (only for dockerProxy)
		if toolName == "dockerProxy" {
			mws = append(mws, ownership.ForDockerProxy())
		}

		// Innermost for towline tools: keep handlers.EnvID current
		if strings.HasPrefix(toolName, "towline_") {
			mws = append(mws, envResolver.Bind(&handlers.EnvID))
		}

		return mws
	}

	// Register upstream features with middleware wrapping
	// We use AddToolWrapped instead of the upstream Add*Features methods
	registerWrappedUpstreamTools(srv, wrappers)

	// Setup proxy manager (auto-detect or use the configured backend)
	setupProxyManager(handlers, srv, o.Stack, o.Proxy, o.CaddyAPI)

	// Register towline-specific tools
	registerTowlineTools(srv, handlers, wrappers)

	return &Instance{Server: srv, Store: approvalStore}, nil
}

// resolveStackID attempts to resolve the stack name to an ID at startup.
func resolveStackID(srv *mcp.PortainerMCPServer, scoping *middleware.StackScoping, stackName string) {
	stacks, err := srv.Client().GetLocalStacks()
	if err != nil {
		log.Warn().Err(err).Msg("failed to resolve stack ID at startup, entering pending mode")
		return
	}

	for _, s := range stacks {
		if s.Name == stackName {
			scoping.SetStackID(s.ID)
			log.Info().Int("stack-id", s.ID).Str("stack-name", stackName).Msg("resolved stack ID")
			return
		}
	}

	log.Warn().Str("stack-name", stackName).Msg("stack not found, entering pending mode")
}

// getEnvironmentID extracts the environment ID from a local stack, or
// returns 0 if the stack is not found.
func getEnvironmentID(srv *mcp.PortainerMCPServer, stackName string) int {
	s, _ := findLocalStack(srv, stackName)
	return s.EndpointID
}

// findLocalStack looks up the named local stack.
func findLocalStack(srv *mcp.PortainerMCPServer, stackName string) (models.LocalStack, bool) {
	stacks, err := srv.Client().GetLocalStacks()
	if err != nil {
		return models.LocalStack{}, false
	}
	for _, s := range stacks {
		if s.Name == stackName {
			return s, true
		}
	}
	return models.LocalStack{}, false
}

// registerWrappedUpstreamTools registers all upstream tools with middleware wrappers.
func registerWrappedUpstreamTools(srv *mcp.PortainerMCPServer, wrappers func(string) []func(mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc) {
	// Local stack tools
	srv.AddToolWrapped("listLocalStacks", srv.HandleGetLocalStacks(), wrappers("listLocalStacks")...)
	srv.AddToolWrapped("getLocalStackFile", srv.HandleGetLocalStackFile(), wrappers("getLocalStackFile")...)

	if !srv.IsReadOnly() {
		srv.AddToolWrapped("createLocalStack", srv.HandleCreateLocalStack(), wrappers("createLocalStack")...)
		srv.AddToolWrapped("updateLocalStack", srv.HandleUpdateLocalStack(), wrappers("updateLocalStack")...)
		srv.AddToolWrapped("startLocalStack", srv.HandleStartLocalStack(), wrappers("startLocalStack")...)
		srv.AddToolWrapped("stopLocalStack", srv.HandleStopLocalStack(), wrappers("stopLocalStack")...)
		srv.AddToolWrapped("deleteLocalStack", srv.HandleDeleteLocalStack(), wrappers("deleteLocalStack")...)
	}

	// Docker proxy
	srv.AddToolWrapped("dockerProxy", srv.HandleDockerProxy(), wrappers("dockerProxy")...)

	// We intentionally do NOT register environment, team, user, access group,
	// edge stack, kubernetes, tag, or settings tools — they are admin-level
	// operations that should not be available to project-scoped agents.
	// The agent only needs local stack management and Docker proxy.
}

// setupProxyManager configures the proxy backend on the handlers.
// If an explicit flag is provided, that backend is used.
// Otherwise it auto-detects from the compose file (checks for traefik labels).
func setupProxyManager(h *towline.Handlers, srv *mcp.PortainerMCPServer, stackName, proxyFlag, caddyAPI string) {
	if proxyFlag != "" {
		switch proxy.Backend(proxyFlag) {
		case proxy.BackendTraefik:
			h.ProxyManager = &proxy.TraefikManager{StackName: stackName}
			log.Info().Str("proxy", "traefik").Msg("proxy backend configured via flag")
		case proxy.BackendCaddy:
			cm := proxy.NewCaddyManager(caddyAPI, stackName)
			h.ProxyManager = cm
			h.CaddyManager = cm
			log.Info().Str("proxy", "caddy").Str("api", caddyAPI).Msg("proxy backend configured via flag")
		case proxy.BackendCloudflare:
			h.ProxyManager = &proxy.CloudflareManager{StackName: stackName}
			log.Info().Str("proxy", "cloudflare").Msg("proxy backend configured via flag")
		default:
			log.Warn().Str("proxy", proxyFlag).Msg("unknown proxy backend, will require explicit method parameter")
		}
		return
	}

	// Auto-detect from compose file
	stacks, err := srv.Client().GetLocalStacks()
	if err != nil {
		log.Warn().Err(err).Msg("cannot auto-detect proxy backend: failed to get stacks")
		return
	}

	for _, s := range stacks {
		if s.Name != stackName {
			continue
		}

		compose, err := srv.Client().GetLocalStackFile(s.ID)
		if err != nil {
			log.Warn().Err(err).Msg("cannot auto-detect proxy backend: failed to get compose file")
			return
		}

		if strings.Contains(compose, "traefik.http.routers") || strings.Contains(compose, "traefik.enable") {
			h.ProxyManager = &proxy.TraefikManager{StackName: stackName}
			log.Info().Str("proxy", "traefik").Msg("proxy backend auto-detected from compose labels")
			return
		}

		break
	}

	log.Info().Msg("no proxy backend detected; domain tools will require explicit method parameter")
}

// registerTowlineTools registers all towline-specific tools with the MCP server.
func registerTowlineTools(srv *mcp.PortainerMCPServer, h *towline.Handlers, wrappers func(string) []func(mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc) {
	srv.AddToolWrapped("towline_service_health", h.HandleServiceHealth(), wrappers("towline_service_health")...)
	srv.AddToolWrapped("towline_service_logs", h.HandleServiceLogs(), wrappers("towline_service_logs")...)
	srv.AddToolWrapped("towline_env_get", h.HandleEnvGet(), wrappers("towline_env_get")...)
	srv.AddToolWrapped("towline_env_set", h.HandleEnvSet(), wrappers("towline_env_set")...)
	srv.AddToolWrapped("towline_domains_list", h.HandleDomainsList(), wrappers("towline_domains_list")...)
	srv.AddToolWrapped("towline_domains_add", h.HandleDomainsAdd(), wrappers("towline_domains_add")...)
	srv.AddToolWrapped("towline_domains_remove", h.HandleDomainsRemove(), wrappers("towline_domains_remove")...)
	srv.AddToolWrapped("towline_scale", h.HandleScale(), wrappers("towline_scale")...)
	srv.AddToolWrapped("towline_deployments", h.HandleDeployments(), wrappers("towline_deployments")...)
	srv.AddToolWrapped("towline_exec", h.HandleExec(), wrappers("towline_exec")...)
}
