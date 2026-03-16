package main

import (
	"flag"
	"io"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/internal/tooldef"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

const (
	defaultToolsPath        = "tools.yaml"
	defaultTowlineToolsPath = "towline-tools.yaml"
)

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	log.Info().
		Str("version", Version).
		Str("build-date", BuildDate).
		Str("commit", Commit).
		Msg("Towline MCP server")

	// Upstream flags
	serverFlag := flag.String("server", "", "The Portainer server URL")
	tokenFlag := flag.String("token", "", "The authentication token for the Portainer server")
	toolsFlag := flag.String("tools", "", "The path to the tools YAML file")
	readOnlyFlag := flag.Bool("read-only", false, "Run in read-only mode")
	disableVersionCheckFlag := flag.Bool("disable-version-check", true, "Disable Portainer server version check")

	// Towline flags
	stackFlag := flag.String("stack", "", "Stack name to scope this MCP instance to")
	tierFlag := flag.String("tier", "", "Deployment tier: dev or prod")
	proxyFlag := flag.String("proxy", "", "Proxy backend: traefik, caddy, or cloudflare (auto-detected if omitted)")
	caddyAPIFlag := flag.String("caddy-api", "http://localhost:2019", "Caddy admin API URL")

	flag.Parse()

	if *serverFlag == "" || *tokenFlag == "" {
		log.Fatal().Msg("Both -server and -token flags are required")
	}
	if *stackFlag == "" || *tierFlag == "" {
		log.Fatal().Msg("Both -stack and -tier flags are required")
	}

	tier := middleware.Tier(*tierFlag)
	if tier != middleware.TierDev && tier != middleware.TierProd {
		log.Fatal().Msg("-tier must be 'dev' or 'prod'")
	}

	// Handle tools.yaml files
	toolsPath := *toolsFlag
	if toolsPath == "" {
		toolsPath = defaultToolsPath
	}
	exists, err := tooldef.CreateToolsFileIfNotExists(toolsPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create tools.yaml file")
	}
	if exists {
		log.Info().Msg("using existing tools.yaml file")
	} else {
		log.Info().Msg("created tools.yaml file")
	}

	towlineToolsPath := defaultTowlineToolsPath
	_, err = tooldef.CreateTowlineToolsFileIfNotExists(towlineToolsPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create towline-tools.yaml file")
	}

	log.Info().
		Str("portainer-host", *serverFlag).
		Str("stack", *stackFlag).
		Str("tier", *tierFlag).
		Msg("starting Towline MCP server")

	// Create the upstream server
	srv, err := mcp.NewPortainerMCPServer(
		*serverFlag, *tokenFlag, toolsPath,
		mcp.WithReadOnly(*readOnlyFlag),
		mcp.WithDisableVersionCheck(*disableVersionCheckFlag),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create server")
	}

	// Load and merge towline tools
	towlineTools, err := toolgen.LoadToolsFromYAML(towlineToolsPath, "v1.0.0")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load towline tools")
	}
	srv.MergeTools(towlineTools)

	// Create middleware instances
	approvalStore := approval.NewStore()
	stackScoping := middleware.NewStackScoping(*stackFlag)

	// Resolve stack ID at startup
	resolveStackID(srv, stackScoping, *stackFlag)

	// Helper to create proxy function for ownership checks
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		resp, err := srv.Client().ProxyDockerRequest(opts)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}

	// Get environment ID from first stack or default to 0
	envID := getEnvironmentID(srv, *stackFlag)
	ownership := middleware.NewContainerOwnership(*stackFlag, envID, proxyFn)

	// Build middleware chain for a given tool
	wrappers := func(toolName string) []func(mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
		var mws []func(mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc

		// Outermost: tier gating
		mws = append(mws, middleware.NewTierGating(tier, approvalStore, toolName))

		// Stack scoping
		mws = append(mws, stackScoping.ForTool(toolName))

		// Container ownership (only for dockerProxy)
		if toolName == "dockerProxy" {
			mws = append(mws, ownership.ForDockerProxy())
		}

		return mws
	}

	// Register upstream features with middleware wrapping
	// We use AddToolWrapped instead of the upstream Add*Features methods
	registerWrappedUpstreamTools(srv, wrappers)

	// Register towline-specific tools (implemented in later tasks, stubs for now)
	// TODO: registerTowlineTools(srv, wrappers, ...)

	_ = proxyFlag    // Used in later tasks (proxy detection)
	_ = caddyAPIFlag // Used in later tasks (Caddy backend)

	err = srv.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}

	approvalStore.Stop()
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

// getEnvironmentID extracts the environment ID from a local stack.
func getEnvironmentID(srv *mcp.PortainerMCPServer, stackName string) int {
	stacks, err := srv.Client().GetLocalStacks()
	if err != nil {
		return 0
	}
	for _, s := range stacks {
		if s.Name == stackName {
			return s.EndpointID
		}
	}
	return 0
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
